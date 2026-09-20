package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // migration driver, registered via side-effect import
	_ "github.com/golang-migrate/migrate/v4/source/file"       // migration source, registered via side-effect import
	"github.com/jackc/pgx/v5/pgxpool"

	"to-do-list/internal/auth/password"
	"to-do-list/internal/auth/token"
	"to-do-list/internal/buildinfo"
	"to-do-list/internal/config"
	"to-do-list/internal/logger"
	"to-do-list/internal/observability"
	"to-do-list/internal/repository/postgres"
	httpapi "to-do-list/internal/transport/http"
	"to-do-list/internal/transport/httpserver"
	"to-do-list/internal/usecase"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(1)
	}

	build := buildinfo.Read()

	log := logger.New(os.Stdout, cfg.LogLevel, cfg.LogFormat).With(
		"service", cfg.AppName,
		"version", build.Version,
		"commit", build.Commit,
	)

	if err := run(cfg, log, build); err != nil {
		log.Error("server exited with an error", "error", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, log *slog.Logger, build buildinfo.Info) error {
	// Two contexts, and the difference is the whole of graceful shutdown.
	// signalCtx is cancelled the moment SIGTERM arrives; it only decides when
	// to start stopping. Requests hang off requestCtx, which stays alive
	// through the grace period, so a signal does not cancel the work the
	// shutdown is supposed to be waiting for.
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	requestCtx, abandonInFlight := context.WithCancel(context.Background())
	defer abandonInFlight()

	if cfg.RunMigrations {
		log.Info("running database migrations", "path", cfg.MigrationsPath)
		if err := runMigrations(cfg); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
	}

	pool, err := postgres.NewPool(signalCtx, cfg.DatabaseDSN(), postgres.PoolConfig{
		MaxConns:       int32(cfg.DBMaxConns),
		MinConns:       int32(cfg.DBMinConns),
		MaxConnLife:    cfg.DBMaxConnLife,
		MaxConnIdle:    cfg.DBMaxConnIdle,
		ConnectTimeout: cfg.DBConnectTimeout,
	})
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	log.Info("connected to the database",
		"max_connections", cfg.DBMaxConns,
		"min_connections", cfg.DBMinConns,
		"max_conn_lifetime", cfg.DBMaxConnLife,
	)

	metrics := observability.New(cfg.MetricsNamespace, build)
	metrics.Preload(knownMetricLabels())
	if err := metrics.Register(observability.NewPoolCollector(cfg.MetricsNamespace, poolStats(pool))); err != nil {
		return fmt.Errorf("register pool metrics: %w", err)
	}

	taskRepo := postgres.NewTaskRepository(pool)
	userRepo := postgres.NewUserRepository(pool)
	refreshRepo := postgres.NewRefreshTokenRepository(pool)

	tokenService, err := token.NewService(cfg.JWTSecret, cfg.JWTTTL, cfg.JWTIssuer, cfg.JWTMinSecretLength)
	if err != nil {
		return fmt.Errorf("build token service: %w", err)
	}

	hasher, err := password.NewHasher(cfg.PasswordBcryptCost, cfg.PasswordMaxConcurrent)
	if err != nil {
		return fmt.Errorf("build password hasher: %w", err)
	}

	taskUC := usecase.NewTaskUseCase(taskRepo, usecase.TaskConfig{
		Timeout:              cfg.DBCallTimeout,
		MaxTitleLength:       cfg.TaskMaxTitleLength,
		MaxDescriptionLength: cfg.TaskMaxDescriptionLength,
		DefaultPageSize:      cfg.DefaultPageSize,
		MaxPageSize:          cfg.MaxPageSize,
	}, log)

	userUC := usecase.NewUserUseCase(userRepo, hasher, usecase.UserConfig{
		Timeout:           cfg.DBCallTimeout,
		MinUsernameLength: cfg.UsernameMinLength,
		MaxUsernameLength: cfg.UsernameMaxLength,
		MinPasswordLength: cfg.PasswordMinLength,
		AdminUsername:     cfg.AdminUsername,
		DefaultPageSize:   cfg.DefaultPageSize,
		MaxPageSize:       cfg.MaxPageSize,
	}, log, usecase.WithMetrics(metrics))

	authUC := usecase.NewAuthUseCase(userRepo, tokenService, refreshRepo, token.NewIssuer(), hasher, usecase.AuthConfig{
		AdminUsername:     cfg.AdminUsername,
		AdminPasswordHash: cfg.AdminPasswordHash,
		Timeout:           cfg.DBCallTimeout,
		MinUsernameLength: cfg.UsernameMinLength,
		MaxUsernameLength: cfg.UsernameMaxLength,
		MinPasswordLength: cfg.PasswordMinLength,
		RefreshTTL:        cfg.JWTRefreshTTL,
	}, log, usecase.WithMetrics(metrics))

	cleaner := usecase.NewSessionCleaner(refreshRepo, usecase.SessionCleanupConfig{
		Interval:  cfg.CleanupInterval,
		Retention: cfg.RefreshTokenRetention,
		Timeout:   cfg.DBCallTimeout,
	}, log, usecase.WithMetrics(metrics))

	// The janitor is background work nobody is waiting for, so it stops with
	// the signal rather than with the last request.
	cleanupCtx, stopCleanup := context.WithCancel(signalCtx)
	var cleanupDone sync.WaitGroup
	defer cleanupDone.Wait()
	defer stopCleanup()

	cleanupDone.Add(1)
	go func() {
		defer cleanupDone.Done()
		cleaner.Run(cleanupCtx)
	}()

	app := httpserver.New(
		requestCtx,
		httpserver.Config{
			AppName:                  cfg.AppName,
			ReadTimeout:              cfg.ReadTimeout,
			WriteTimeout:             cfg.WriteTimeout,
			MaxBodyBytes:             cfg.MaxBodyBytes,
			TrustedProxies:           cfg.TrustedProxies,
			ProxyHeader:              cfg.ProxyHeader,
			CORSAllowOrigins:         cfg.CORSAllowOrigins,
			CORSAllowMethods:         cfg.CORSAllowMethods,
			CORSAllowHeaders:         cfg.CORSAllowHeaders,
			RateLimitAuthMaxRequests: cfg.RateLimitAuthMaxRequests,
			RateLimitAuthWindow:      cfg.RateLimitAuthWindow,
			HealthReadyTimeout:       cfg.HealthReadyTimeout,
			MetricsEnabled:           cfg.MetricsEnabled,
			MetricsPath:              cfg.MetricsPath,
			MetricsAddress:           cfg.MetricsAddress,
		},
		log,
		httpserver.Observability{
			Requests: metrics,
			Exporter: metrics.Handler(),
			Build:    build,
		},
		authUC,
		taskUC,
		userUC,
		httpapi.Check{Name: "postgres", Probe: pool.Ping},
	)

	// The listener is opened before anything reports success: a metrics port
	// that is already taken must stop the process, not leave a service that
	// passes every health check and exports nothing.
	var metricsServer *httpserver.MetricsServer
	metricsErr := make(chan error, 1)
	if cfg.MetricsEnabled && cfg.MetricsAddress != "" {
		metricsServer, err = httpserver.NewMetricsServer(cfg.MetricsAddress, cfg.MetricsPath, metrics.Handler())
		if err != nil {
			return fmt.Errorf("start metrics listener: %w", err)
		}
		log.Info("serving metrics on a separate listener", "address", metricsServer.Addr(), "path", cfg.MetricsPath)
		go func() { metricsErr <- metricsServer.Serve() }()
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening",
			"address", cfg.ServerAddress,
			"metrics_enabled", cfg.MetricsEnabled,
			"metrics_path", cfg.MetricsPath,
			"built_at", build.BuiltAt,
			"go_version", build.GoVersion,
		)
		if err := app.Listen(cfg.ServerAddress); err != nil {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	return awaitStop(stopSignals{
		signal:     signalCtx.Done(),
		serve:      serveErr,
		metrics:    metricsErr,
		hasMetrics: metricsServer != nil,
	}, func() error {
		// Both servers stop accepting and drain at once, sharing one budget;
		// anything still running when it runs out is cancelled afterwards,
		// which is the first moment a request context is touched. The pool
		// outlives this call - it is closed by the deferred Close above,
		// after Drain has cancelled whatever was still holding a connection.
		return httpserver.Drain(cfg.ShutdownTimeout, abandonInFlight, app, metricsServer)
	}, cfg.ShutdownTimeout, log)
}

// stopSignals is everything that can end a run: the shutdown signal, and the
// two listeners, each of which reports exactly once on its channel.
type stopSignals struct {
	signal     <-chan struct{}
	serve      <-chan error
	metrics    <-chan error
	hasMetrics bool
}

// awaitStop waits for the first of those, then leaves through the one door
// all three share.
//
// A listener that dies on its own is still a shutdown. Returning straight
// away, as this used to, left the other server accepting connections and
// closed the database pool underneath every request still running - so a
// metrics port that was already taken would cut off the API's in-flight
// work, and an API listener that failed would leave a process serving
// nothing but /metrics. Here the reason is recorded and the draining is the
// same draining a signal gets.
func awaitStop(stops stopSignals, drain func() error, grace time.Duration, log *slog.Logger) error {
	var (
		exitErr         error
		serveReported   bool
		metricsReported bool
	)

	select {
	case err := <-stops.serve:
		serveReported = true
		if err != nil {
			log.Error("the API listener stopped on its own; shutting the rest down", "error", err)
			exitErr = fmt.Errorf("listen: %w", err)
		}

	case err := <-stops.metrics:
		metricsReported = true
		if err != nil {
			log.Error("the metrics listener stopped on its own; shutting the rest down", "error", err)
			exitErr = fmt.Errorf("metrics listener: %w", err)
		}

	case <-stops.signal:
		log.Info("shutdown signal received, no longer accepting connections", "grace_period", grace)
	}

	drainErr := drain()

	// Wait for the goroutines that have not reported yet, so none of them is
	// still running when the process leaves. The one that woke the select
	// has already reported; reading it again would hang.
	if !serveReported {
		<-stops.serve
	}
	if stops.hasMetrics && !metricsReported {
		<-stops.metrics
	}

	if drainErr != nil {
		log.Warn("some work did not finish within the grace period", "error", drainErr)
	}

	switch {
	case exitErr != nil:
		// The failure that started this is the answer. A drain error on the
		// way out is a consequence of it, logged just above and not worth
		// replacing the cause with.
		return exitErr
	case drainErr != nil:
		return fmt.Errorf("graceful shutdown: %w", drainErr)
	}

	log.Info("shutdown complete, every in-flight request finished")
	return nil
}

func knownMetricLabels() observability.KnownLabels {
	return observability.KnownLabels{
		AuthOperations: []string{usecase.OperationRegister, usecase.OperationLogin},
		AuthOutcomes: []string{
			usecase.OutcomeSuccess,
			usecase.OutcomeRejected,
			usecase.OutcomeFailure,
		},
		RotationOutcomes: []string{
			usecase.OutcomeSuccess,
			usecase.OutcomeReuse,
			usecase.OutcomeUnknown,
			usecase.OutcomeExpired,
			usecase.OutcomeFailure,
		},
		RevocationReasons: []string{
			usecase.ReasonLogout,
			usecase.ReasonTokenReuse,
			usecase.ReasonPasswordChange,
		},
		CleanupOutcomes: []string{
			usecase.OutcomeSuccess,
			usecase.OutcomeFailure,
		},
	}
}

func poolStats(pool *pgxpool.Pool) func() observability.PoolStats {
	return func() observability.PoolStats {
		s := pool.Stat()
		return observability.PoolStats{
			AcquiredConns:        s.AcquiredConns(),
			IdleConns:            s.IdleConns(),
			TotalConns:           s.TotalConns(),
			MaxConns:             s.MaxConns(),
			ConstructingConns:    s.ConstructingConns(),
			AcquireCount:         s.AcquireCount(),
			EmptyAcquireCount:    s.EmptyAcquireCount(),
			CanceledAcquireCount: s.CanceledAcquireCount(),
			AcquireDuration:      s.AcquireDuration(),
		}
	}
}

func runMigrations(cfg config.Config) error {
	m, err := migrate.New("file://"+cfg.MigrationsPath, cfg.DatabaseDSN())
	if err != nil {
		return fmt.Errorf("initialise migrator: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			fmt.Fprintln(os.Stderr, "migrator: close source:", srcErr)
		}
		if dbErr != nil {
			fmt.Fprintln(os.Stderr, "migrator: close database connection:", dbErr)
		}
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return nil
}
