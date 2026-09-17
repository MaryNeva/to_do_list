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

	"to-do-list/internal/auth/password"
	"to-do-list/internal/auth/token"
	"to-do-list/internal/config"
	"to-do-list/internal/logger"
	"to-do-list/internal/repository/postgres"
	"to-do-list/internal/transport/httpserver"
	"to-do-list/internal/usecase"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(1)
	}

	log := logger.New(os.Stdout, cfg.LogLevel, cfg.LogFormat)

	if err := run(cfg, log); err != nil {
		log.Error("server exited with an error", "error", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, log *slog.Logger) error {
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
	}, log)

	authUC := usecase.NewAuthUseCase(userRepo, tokenService, refreshRepo, token.NewIssuer(), hasher, usecase.AuthConfig{
		AdminUsername:     cfg.AdminUsername,
		AdminPasswordHash: cfg.AdminPasswordHash,
		Timeout:           cfg.DBCallTimeout,
		MinUsernameLength: cfg.UsernameMinLength,
		MaxUsernameLength: cfg.UsernameMaxLength,
		MinPasswordLength: cfg.PasswordMinLength,
		RefreshTTL:        cfg.JWTRefreshTTL,
	}, log)

	cleaner := usecase.NewSessionCleaner(refreshRepo, usecase.SessionCleanupConfig{
		Interval:  cfg.CleanupInterval,
		Retention: cfg.RefreshTokenRetention,
		Timeout:   cfg.DBCallTimeout,
	}, log)

	// The cleaner gets its own cancellable context so it is stopped on every
	// return path, not only on a shutdown signal. The defers are registered
	// in this order on purpose: they run last-in-first-out, so the goroutine
	// is told to stop before anything waits for it.
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
		},
		log,
		authUC,
		taskUC,
		userUC,
		pool.Ping,
	)

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "address", cfg.ServerAddress)
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
