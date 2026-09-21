package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
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
		wantCode   string
	}{
		{"not found", apperr.ErrNotFound, fiber.StatusNotFound, CodeNotFound},
		{"conflict", apperr.ErrConflict, fiber.StatusConflict, CodeConflict},
		{"validation", apperr.ErrValidation, fiber.StatusBadRequest, CodeValidation},
		{"invalid credentials", apperr.ErrInvalidCredentials, fiber.StatusUnauthorized, CodeInvalidCredentials},
		{"unauthorized", apperr.ErrUnauthorized, fiber.StatusUnauthorized, CodeUnauthorized},
		{"forbidden", apperr.ErrForbidden, fiber.StatusForbidden, CodeForbidden},
		{"wrapped not found", errWrap(apperr.ErrNotFound, "task 5"), fiber.StatusNotFound, CodeNotFound},
		{"unknown error hidden as 500", errors.New("pq: syntax error near GROUP"), fiber.StatusInternalServerError, CodeInternal},
		{"fiber error carries its status", fiber.NewError(fiber.StatusTooManyRequests, "slow down"), fiber.StatusTooManyRequests, CodeRateLimited},
		{"fiber error with no code of its own", fiber.NewError(fiber.StatusUnsupportedMediaType, "send JSON"), fiber.StatusUnsupportedMediaType, CodeBadRequest},
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

			raw, _ := io.ReadAll(resp.Body)
			var body struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("body is not JSON: %v (%s)", err, raw)
			}
			if body.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", body.Code, tt.wantCode)
			}
			if body.Message == "" {
				t.Error("the envelope carries no message")
			}
		})
	}
}

func TestErrorHandler_ValidationErrorsNameTheirFields(t *testing.T) {
	type payload struct {
		Title string `json:"title" validate:"required"`
		Email string `json:"email" validate:"required,email"`
	}

	v := validator.New()
	v.RegisterTagNameFunc(func(field reflect.StructField) string {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		return name
	})

	app := appWithHandlerError(v.Struct(payload{Email: "not-an-email"}))
	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Code   string `json:"code"`
		Fields []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, raw)
	}

	if body.Code != CodeValidation {
		t.Errorf("code = %q, want %q", body.Code, CodeValidation)
	}

	var named []string
	for _, f := range body.Fields {
		if f.Message == "" {
			t.Errorf("field %q carries no message", f.Field)
		}
		named = append(named, f.Field)
	}
	if !slices.Equal(named, []string{"title", "email"}) {
		t.Errorf("fields = %v, want the JSON names [title email] (%s)", named, raw)
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

func TestErrorHandler_MessageDropsTheSentinelPrefix(t *testing.T) {
	app := appWithHandlerError(fmt.Errorf("%w: title must not be empty", apperr.ErrValidation))

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}

	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, raw)
	}
	if body.Code != CodeValidation {
		t.Errorf("code = %q, want %q", body.Code, CodeValidation)
	}
	if body.Message != "title must not be empty" {
		t.Errorf("message = %q, want it without the sentinel prefix", body.Message)
	}
}

func TestErrorHandler_ATimeoutIsRetryableRatherThanInternal(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"the deadline on a database call expired", context.DeadlineExceeded},
		{"the work was abandoned during shutdown", context.Canceled},
		{
			name: "wrapped, the way a repository reports it",
			err:  fmt.Errorf("postgres: query tasks: %w", context.DeadlineExceeded),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := appWithHandlerError(tc.err)
			resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/x", nil))
			if err != nil {
				t.Fatalf("app.Test() unexpected error: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != fiber.StatusServiceUnavailable {
				t.Errorf("status = %d, want %d", resp.StatusCode, fiber.StatusServiceUnavailable)
			}
			if retry := resp.Header.Get(fiber.HeaderRetryAfter); retry == "" {
				t.Error("a 503 without Retry-After leaves a client to guess when to come back")
			}

			raw, _ := io.ReadAll(resp.Body)
			var body struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("body is not JSON: %v (%s)", err, raw)
			}
			if body.Code != CodeUnavailable {
				t.Errorf("code = %q, want %q", body.Code, CodeUnavailable)
			}
			if strings.Contains(body.Message, "postgres") {
				t.Errorf("the message names the dependency: %q", body.Message)
			}
		})
	}
}

func TestErrorHandler_LogsATimeoutAsAWarning(t *testing.T) {
	var recorded strings.Builder
	logged := slog.New(slog.NewTextHandler(&recorded, &slog.HandlerOptions{Level: slog.LevelDebug}))

	app := fiber.New(fiber.Config{ErrorHandler: NewErrorHandler(logged)})
	app.Get("/x", func(c *fiber.Ctx) error {
		return fmt.Errorf("postgres: query tasks: %w", context.DeadlineExceeded)
	})

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	defer resp.Body.Close()

	line := recorded.String()
	if !strings.Contains(line, "level=WARN") {
		t.Errorf("the timeout was not logged as a warning: %s", line)
	}
	// The log keeps the detail hidden from the client.
	if !strings.Contains(line, "postgres: query tasks") {
		t.Errorf("the log does not say what timed out: %s", line)
	}
}
