package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

type fakeValidator struct {
	claims domain.Claims
	err    error
}

func (f fakeValidator) ValidateToken(_ context.Context, _ string) (domain.Claims, error) {
	return f.claims, f.err
}

func newTestApp(validator TokenValidator) *fiber.App {
	app := fiber.New()
	app.Get("/protected", Auth(validator), func(c *fiber.Ctx) error {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			return errors.New("claims missing from context")
		}
		return c.JSON(fiber.Map{"user_id": claims.UserID})
	})
	return app
}

func TestAuth_MissingHeader(t *testing.T) {
	app := newTestApp(fakeValidator{})

	req := httptest.NewRequest(fiber.MethodGet, "/protected", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}

	if resp.StatusCode == fiber.StatusOK {
		t.Error("request with no Authorization header must not reach the handler")
	}
}

func TestAuth_MalformedHeader(t *testing.T) {
	tests := []string{
		"Token abc123", // wrong scheme
		"Bearer",       // missing token
		"Bearerabc123", // missing separator
	}

	for _, header := range tests {
		t.Run(header, func(t *testing.T) {
			app := newTestApp(fakeValidator{})
			req := httptest.NewRequest(fiber.MethodGet, "/protected", nil)
			req.Header.Set(fiber.HeaderAuthorization, header)

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() unexpected error: %v", err)
			}
			if resp.StatusCode == fiber.StatusOK {
				t.Errorf("header %q should not authenticate", header)
			}
		})
	}
}

func TestAuth_InvalidToken(t *testing.T) {
	app := newTestApp(fakeValidator{err: apperr.ErrUnauthorized})

	req := httptest.NewRequest(fiber.MethodGet, "/protected", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer whatever-invalid")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode == fiber.StatusOK {
		t.Error("an invalid token must not authenticate")
	}
}

func TestAuth_ValidToken_SetsClaims(t *testing.T) {
	want := domain.Claims{UserID: 7, Username: "alice"}
	app := newTestApp(fakeValidator{claims: want})

	req := httptest.NewRequest(fiber.MethodGet, "/protected", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer a-valid-token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
}

func TestAuth_CaseInsensitiveBearerScheme(t *testing.T) {
	want := domain.Claims{UserID: 1, Username: "alice"}
	app := newTestApp(fakeValidator{claims: want})

	req := httptest.NewRequest(fiber.MethodGet, "/protected", nil)
	req.Header.Set(fiber.HeaderAuthorization, "bearer a-valid-token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want %d ('bearer' lowercase should still be accepted)", resp.StatusCode, fiber.StatusOK)
	}
}
