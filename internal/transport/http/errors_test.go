package http

import (
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func appWithHandlerError(err error) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: NewErrorHandler(silentLogger())})
	app.Get("/x", func(c *fiber.Ctx) error { return err })
	return app
}

func TestErrorHandler_MapsSentinelErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"not found", apperr.ErrNotFound, fiber.StatusNotFound},
		{"conflict", apperr.ErrConflict, fiber.StatusConflict},
		{"validation", apperr.ErrValidation, fiber.StatusBadRequest},
		{"invalid credentials", apperr.ErrInvalidCredentials, fiber.StatusUnauthorized},
		{"unauthorized", apperr.ErrUnauthorized, fiber.StatusUnauthorized},
		{"forbidden", apperr.ErrForbidden, fiber.StatusForbidden},
		{"wrapped not found", errWrap(apperr.ErrNotFound, "task 5"), fiber.StatusNotFound},
		{"unknown error hidden as 500", errors.New("pq: syntax error near GROUP"), fiber.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := appWithHandlerError(tt.err)
			resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/x", nil))
			if err != nil {
				t.Fatalf("app.Test() unexpected error: %v", err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

func TestErrorHandler_HidesInternalErrorDetails(t *testing.T) {
	app := appWithHandlerError(errors.New("pq: connection to db failed at 10.0.0.5:5432"))

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}

	var buf [512]byte
	n, _ := resp.Body.Read(buf[:])
	body := string(buf[:n])

	if body == "" {
		t.Fatal("expected a JSON error body")
	}
	if strings.Contains(body, "10.0.0.5") {
		t.Errorf("internal error details leaked to the client: %s", body)
	}
}

func TestErrorHandler_PassesThroughFiberErrors(t *testing.T) {
	app := appWithHandlerError(fiber.NewError(fiber.StatusTeapot, "I'm a teapot"))

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusTeapot {
		t.Errorf("status = %d, want %d", resp.StatusCode, fiber.StatusTeapot)
	}
}

func errWrap(err error, msg string) error {
	return &wrappedErr{msg: msg, err: err}
}

type wrappedErr struct {
	msg string
	err error
}

func (w *wrappedErr) Error() string { return w.msg + ": " + w.err.Error() }
func (w *wrappedErr) Unwrap() error { return w.err }
