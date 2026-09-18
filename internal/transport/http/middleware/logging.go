package middleware

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"

	httpapi "to-do-list/internal/transport/http"
)

func RequestLogger(logger *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		err := c.Next()

		status := c.Response().StatusCode()
		if err != nil {
			status = httpapi.StatusFor(err)
		}

		level := slog.LevelInfo
		switch {
		case status >= fiber.StatusInternalServerError:
			level = slog.LevelError
		case status >= fiber.StatusBadRequest:
			level = slog.LevelWarn
		}

		logger.LogAttrs(c.UserContext(), level, "http_request",
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
			slog.Int("status", status),
			slog.Duration("latency", time.Since(start)),
			slog.String("ip", c.IP()),
		)

		return err
	}
}
