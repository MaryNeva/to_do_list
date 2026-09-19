package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/requestid"

	"to-do-list/internal/apperr"
	logpkg "to-do-list/internal/logger"
)

func loggingApp(w *bytes.Buffer) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			return c.Status(500).SendString(err.Error())
		},
	})
	app.Use(requestid.New())
	app.Use(RequestContext(context.Background()))
	app.Use(RequestLogger(logpkg.New(w, "info", "json")))
	app.Get("/ok", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/missing", func(c *fiber.Ctx) error { return apperr.ErrNotFound })
	return app
}

func lastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &record); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, lines[len(lines)-1])
	}

	return record
}

func TestRequestLogger_CarriesTheRequestID(t *testing.T) {
	var buf bytes.Buffer
	app := loggingApp(&buf)

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/ok", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	record := lastLine(t, &buf)
	got, present := record["request_id"].(string)
	if !present || got == "" {
		t.Fatalf("request_id missing from %v", record)
	}
	if want := resp.Header.Get(fiber.HeaderXRequestID); got != want {
		t.Errorf("request_id = %q, want the response header %q", got, want)
	}
}

func TestRequestLogger_RecordsTheStatusTheClientWillSee(t *testing.T) {
	var buf bytes.Buffer
	app := loggingApp(&buf)

	if _, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/missing", nil)); err != nil {
		t.Fatalf("request: %v", err)
	}

	record := lastLine(t, &buf)
	if got := record["status"]; got != float64(fiber.StatusNotFound) {
		t.Errorf("status = %v, want 404", got)
	}
	if got := record["level"]; got != slog.LevelWarn.String() {
		t.Errorf("level = %v, want %v - a 404 is a warning, not an error", got, slog.LevelWarn)
	}
}
