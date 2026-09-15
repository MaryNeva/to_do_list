package httpserver

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"

	"to-do-list/internal/domain"
	httpapi "to-do-list/internal/transport/http"
	"to-do-list/internal/transport/http/handler"
	appmiddleware "to-do-list/internal/transport/http/middleware"
)

type Config struct {
	CORSAllowOrigins string
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
}

func New(cfg Config, logger *slog.Logger, authSvc domain.AuthService, taskSvc domain.TaskService, userSvc domain.UserService, dbPing httpapi.Pinger) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "to-do-list",
		ErrorHandler: httpapi.NewErrorHandler(logger),
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	app.Use(recover.New())
	app.Use(requestid.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.CORSAllowOrigins,
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET, POST, PATCH, DELETE, OPTIONS",
	}))
	app.Use(appmiddleware.RequestLogger(logger))
	app.Use(func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Next()
	})

	app.Get("/healthz", httpapi.HealthHandler())
	app.Get("/readyz", httpapi.ReadyHandler(dbPing))

	authMiddleware := appmiddleware.Auth(authSvc)

	authLimiter := limiter.New(limiter.Config{
		Max:        20,
		Expiration: time.Minute,
	})

	v1 := app.Group("/api/v1")

	authHandler := handler.NewAuthHandler(authSvc)
	authGroup := v1.Group("/auth")
	authGroup.Post("/register", authLimiter, authHandler.Register)
	authGroup.Post("/login", authLimiter, authHandler.Login)
	authGroup.Get("/me", authMiddleware, authHandler.Me)

	protected := v1.Group("", authMiddleware)

	handler.NewTaskHandler(taskSvc).Mount(protected.Group("/tasks"))
	handler.NewUserHandler(userSvc).Mount(protected.Group("/users"))

	return app
}
