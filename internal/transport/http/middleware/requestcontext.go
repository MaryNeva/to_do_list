package middleware

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/logger"
)

// RequestContext derives every request's context from base, so cancelling
// base stops the work already in flight. Fiber's own UserContext starts as
// context.Background and is never cancelled by anything.
func RequestContext(base context.Context) fiber.Handler {
	if base == nil {
		base = context.Background()
	}

	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithCancel(base)
		defer cancel()

		var request context.Context = ctx
		if previous := c.UserContext(); previous != nil && previous != context.Background() {
			request = inherited{Context: ctx, values: previous}
		}

		if id := requestID(c); id != "" {
			c.Locals(logger.RequestIDKey, id)
			request = logger.WithRequestID(request, id)
		}

		c.SetUserContext(request)

		return c.Next()
	}
}

type inherited struct {
	context.Context
	values context.Context
}

func (i inherited) Value(key any) any {
	if v := i.Context.Value(key); v != nil {
		return v
	}
	return i.values.Value(key)
}

func requestID(c *fiber.Ctx) string {
	if id := c.GetRespHeader(fiber.HeaderXRequestID); id != "" {
		return id
	}
	return c.Get(fiber.HeaderXRequestID)
}
