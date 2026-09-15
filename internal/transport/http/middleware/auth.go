package middleware

import (
	"context"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

type contextKey int

const claimsContextKey contextKey = iota

type TokenValidator interface {
	ValidateToken(ctx context.Context, token string) (domain.Claims, error)
}

const bearerPrefix = "Bearer "

func Auth(validator TokenValidator) fiber.Handler {
	return func(c *fiber.Ctx) error {
		header := c.Get(fiber.HeaderAuthorization)
		if header == "" {
			return fmt.Errorf("%w: missing Authorization header", apperr.ErrUnauthorized)
		}

		if len(header) <= len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
			return fmt.Errorf("%w: Authorization header must be 'Bearer <token>'", apperr.ErrUnauthorized)
		}

		tokenString := strings.TrimSpace(header[len(bearerPrefix):])
		if tokenString == "" {
			return fmt.Errorf("%w: empty bearer token", apperr.ErrUnauthorized)
		}

		claims, err := validator.ValidateToken(c.Context(), tokenString)
		if err != nil {
			return err
		}

		c.Locals(claimsContextKey, claims)
		return c.Next()
	}
}

func ClaimsFromContext(c *fiber.Ctx) (domain.Claims, bool) {
	claims, ok := c.Locals(claimsContextKey).(domain.Claims)
	return claims, ok
}
