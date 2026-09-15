package http

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
)

type Pinger func(ctx context.Context) error

func HealthHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		return JSON(c, fiber.StatusOK, fiber.Map{"status": "ok"})
	}
}

func ReadyHandler(ping Pinger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
		defer cancel()

		if err := ping(ctx); err != nil {
			return JSON(c, fiber.StatusServiceUnavailable, fiber.Map{
				"status": "unavailable",
				"error":  err.Error(),
			})
		}

		return JSON(c, fiber.StatusOK, fiber.Map{"status": "ready"})
	}
}
