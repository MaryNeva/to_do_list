package http

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/buildinfo"
)

type Pinger func(ctx context.Context) error

type Check struct {
	Name  string
	Probe Pinger
}

type checkResult struct {
	Status     string  `json:"status"`
	DurationMS float64 `json:"duration_ms"`
	Error      string  `json:"error,omitempty"`
}

func HealthHandler(build buildinfo.Info) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return JSON(c, fiber.StatusOK, fiber.Map{
			"status": "ok",
			"build":  build,
		})
	}
}

func ReadyHandler(timeout time.Duration, checks ...Check) fiber.Handler {
	return func(c *fiber.Ctx) error {
		results := make(map[string]checkResult, len(checks))
		status := fiber.StatusOK

		for _, check := range checks {
			ctx, cancel := context.WithTimeout(c.UserContext(), timeout)
			start := time.Now()
			err := check.Probe(ctx)
			elapsed := time.Since(start)
			cancel()

			result := checkResult{
				Status:     "ok",
				DurationMS: float64(elapsed.Microseconds()) / 1000,
			}

			if err != nil {
				result.Status = "unavailable"
				result.Error = err.Error()
				status = fiber.StatusServiceUnavailable
			}

			results[check.Name] = result
		}

		body := fiber.Map{"status": "ready", "checks": results}
		if status != fiber.StatusOK {
			body["status"] = "unavailable"
		}

		return JSON(c, status, body)
	}
}
