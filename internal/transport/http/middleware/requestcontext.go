package middleware

import (
	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/logger"
)

func RequestContext() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.GetRespHeader(fiber.HeaderXRequestID)
		if id == "" {
			id = c.Get(fiber.HeaderXRequestID)
		}

		if id != "" {
			c.Locals(logger.RequestIDKey, id)
			c.SetUserContext(logger.WithRequestID(c.UserContext(), id))
		}

		return c.Next()
	}
}
