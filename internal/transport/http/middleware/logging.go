package middleware

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
)

func RequestLogger(logger *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		err := c.Next()

		status := c.Response().StatusCode()
		level := slog.LevelInfo
		switch {
		case status >= fiber.StatusInternalServerError:
			level = slog.LevelError
		case status >= fiber.StatusBadRequest:
			level = slog.LevelWarn
		}

		logger.LogAttrs(c.Context(), level, "http_request",
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
			slog.Int("status", status),
			slog.Duration("latency", time.Since(start)),
			slog.String("ip", c.IP()),
			slog.String("request_id", c.GetRespHeader(fiber.HeaderXRequestID)),
		)

		return err
	}
}
