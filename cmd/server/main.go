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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.RunMigrations {
		log.Info("running database migrations", "path", cfg.MigrationsPath)
		if err := runMigrations(cfg); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
	}

	pool, err := postgres.NewPool(ctx, cfg.DatabaseDSN(), cfg.DBConnectTimeout)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	log.Info("connected to the database")

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

	hasher, err := password.NewHasher(cfg.PasswordBcryptCost)
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

	cleanupCtx, stopCleanup := context.WithCancel(ctx)
	var cleanupDone sync.WaitGroup
	defer cleanupDone.Wait()
	defer stopCleanup()

	cleanupDone.Add(1)
	go func() {
		defer cleanupDone.Done()
		cleaner.Run(cleanupCtx)
	}()

	app := httpserver.New(
		httpserver.Config{
			AppName:                  cfg.AppName,
			ReadTimeout:              cfg.ReadTimeout,
			WriteTimeout:             cfg.WriteTimeout,
			CORSAllowOrigins:         cfg.CORSAllowOrigins,
			CORSAllowMethods:         cfg.CORSAllowMethods,
			CORSAllowHeaders:         cfg.CORSAllowHeaders,
			RateLimitAuthMaxRequests: cfg.RateLimitAuthMaxRequests,
			RateLimitAuthWindow:      cfg.RateLimitAuthWindow,
			HealthReadyTimeout:       cfg.HealthReadyTimeout,
			MetricsEnabled:           cfg.MetricsEnabled,
			MetricsPath:              cfg.MetricsPath,
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

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		return nil

	case <-ctx.Done():
		log.Info("shutdown signal received, draining in-flight requests")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if err := app.ShutdownWithContext(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}

		<-serveErr

		log.Info("shutdown complete")
		return nil
	}
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
