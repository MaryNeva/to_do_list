package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"

	"to-do-list/internal/buildinfo"
	httpapi "to-do-list/internal/transport/http"
	"to-do-list/internal/transport/http/handler"
	appmiddleware "to-do-list/internal/transport/http/middleware"
)

type Config struct {
	AppName                  string
	ReadTimeout              time.Duration
	WriteTimeout             time.Duration
	MaxBodyBytes             int
	TrustedProxies           []string
	ProxyHeader              string
	CORSAllowOrigins         string
	CORSAllowMethods         string
	CORSAllowHeaders         string
	RateLimitAuthMaxRequests int
	RateLimitAuthWindow      time.Duration
	HealthReadyTimeout       time.Duration
	MetricsEnabled           bool
	MetricsPath              string
	MetricsAddress           string
}

type Observability struct {
	Requests appmiddleware.RequestRecorder
	Exporter http.Handler
	Build    buildinfo.Info
}

// New builds the application. Every request context derives from base, which
// must outlive the signal that starts a shutdown: see Drain.
func New(
	base context.Context,
	cfg Config,
	logger *slog.Logger,
	obs Observability,
	authSvc AuthService,
	taskSvc handler.TaskService,
	userSvc handler.UserService,
	checks ...httpapi.Check,
) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:                 cfg.AppName,
		ErrorHandler:            httpapi.NewErrorHandler(logger),
		ReadTimeout:             cfg.ReadTimeout,
		WriteTimeout:            cfg.WriteTimeout,
		BodyLimit:               cfg.MaxBodyBytes,
		ProxyHeader:             cfg.ProxyHeader,
		EnableTrustedProxyCheck: len(cfg.TrustedProxies) > 0,
		TrustedProxies:          cfg.TrustedProxies,
	})

	app.Use(requestid.New())
	app.Use(appmiddleware.RequestContext(base))

	if obs.Requests != nil {
		app.Use(appmiddleware.Metrics(obs.Requests, cfg.MetricsPath))
	}

	app.Use(appmiddleware.RequestLogger(logger))
	app.Use(recover.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.CORSAllowOrigins,
		AllowHeaders: cfg.CORSAllowHeaders,
		AllowMethods: cfg.CORSAllowMethods,
	}))
	app.Use(func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Next()
	})

	app.Get("/healthz", httpapi.HealthHandler(obs.Build))
	app.Get("/readyz", httpapi.ReadyHandler(logger, cfg.HealthReadyTimeout, checks...))

	if cfg.MetricsEnabled && obs.Exporter != nil && cfg.MetricsAddress == "" {
		app.Get(cfg.MetricsPath, adaptor.HTTPHandler(obs.Exporter))
	}

	authMiddleware := appmiddleware.Auth(authSvc)

	authLimiter := limiter.New(limiter.Config{
		Max:        cfg.RateLimitAuthMaxRequests,
		Expiration: cfg.RateLimitAuthWindow,
		// Without this the limiter writes its own plain-text body, which is
		// the one response that would not carry an error code.
		LimitReached: func(*fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many requests, try again later")
		},
	})

	v1 := app.Group("/api/v1")

	authHandler := handler.NewAuthHandler(authSvc)
	authGroup := v1.Group("/auth")
	authGroup.Post("/register", authLimiter, authHandler.Register)
	authGroup.Post("/login", authLimiter, authHandler.Login)
	authGroup.Post("/refresh", authLimiter, authHandler.Refresh)
	authGroup.Post("/logout", authHandler.Logout)
	authGroup.Get("/me", authMiddleware, authHandler.Me)

	protected := v1.Group("", authMiddleware)

	handler.NewTaskHandler(taskSvc).Mount(protected.Group("/tasks"))
	handler.NewUserHandler(userSvc).Mount(protected.Group("/users"))

	return app
}

// AuthService combines the independent handler and middleware contracts at composition.
type AuthService interface {
	handler.AuthService
	appmiddleware.TokenValidator
}
