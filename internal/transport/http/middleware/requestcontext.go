package middleware

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/logger"
)

// RequestContext derives each request context from base, so cancelling base
// cancels in-flight work (Fiber's default context is never cancelled). Values
// set by earlier middleware are kept; their cancellation is not.
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

// inherited takes cancellation from Context and falls back to values for Value.
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
