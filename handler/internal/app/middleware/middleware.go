package middleware

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"to-do-list.com/users/token"
)

type TokenMiddleware struct {
	TokenService token.Service
	config       string
}

type Middleware interface {
	JWTMiddleware() fiber.Handler
}

func (m *TokenMiddleware) JWTMiddleware() fiber.Handler {
	return func(ctx *fiber.Ctx) error {

		authHeader := ctx.Get("Authorization")
		if authHeader == "" {
			return errors.New("Authorization header is missing")
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return errors.New("Invalid Authorization header format")
		}

		tokenString := parts[1]

		user, err := m.TokenService.ValidateToken(tokenString)
		if err != nil {
			return errors.New("Token validation error")
		}

		ctx.Locals("user", user)

		return ctx.Next()
	}
}

func NewTokenMiddleware(tokenService token.Service, config string) *TokenMiddleware {
	return &TokenMiddleware{TokenService: tokenService, config: config}
}
