package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/buildinfo"
)

func probeApp(t *testing.T, checks ...Check) *fiber.App {
	t.Helper()
	app, _ := probeAppWithLogs(t, checks...)
	return app
}

func probeAppWithLogs(t *testing.T, checks ...Check) (*fiber.App, *bytes.Buffer) {
	t.Helper()

	var logs bytes.Buffer
	app := fiber.New(fiber.Config{ErrorHandler: NewErrorHandler(silentLogger())})
	app.Get("/healthz", HealthHandler(buildinfo.Info{Version: "1.2.3", Commit: "abc123"}))
	app.Get("/readyz", ReadyHandler(slog.New(slog.NewJSONHandler(&logs, nil)), time.Second, checks...))

	return app, &logs
}

func call(t *testing.T, app *fiber.App, path string) (int, map[string]any) {
	t.Helper()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, path, nil))
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, raw)
	}

	return resp.StatusCode, body
}

func ok(context.Context) error { return nil }

func TestHealthHandler_AnswersWithoutTouchingDependencies(t *testing.T) {
	status, body := call(t, probeApp(t), "/healthz")

	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["status"] != "ok" {
		t.Errorf("status field = %v, want %q", body["status"], "ok")
	}

	build, isObject := body["build"].(map[string]any)
	if !isObject {
		t.Fatalf("build = %v, want an object", body["build"])
	}
	if build["version"] != "1.2.3" || build["commit"] != "abc123" {
		t.Errorf("build = %v, want the configured version and commit", build)
	}
}

func TestReadyHandler_ReportsEveryDependencySeparately(t *testing.T) {
	status, body := call(t, probeApp(t,
		Check{Name: "postgres", Probe: ok},
		Check{Name: "cache", Probe: ok},
	), "/readyz")

	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["status"] != "ready" {
		t.Errorf("status field = %v, want %q", body["status"], "ready")
	}

	checks, isObject := body["checks"].(map[string]any)
	if !isObject {
		t.Fatalf("checks = %v, want an object", body["checks"])
	}
	for _, name := range []string{"postgres", "cache"} {
		entry, present := checks[name].(map[string]any)
		if !present {
			t.Fatalf("checks[%q] is missing from %v", name, checks)
		}
		if entry["status"] != "ok" {
			t.Errorf("checks[%q].status = %v, want %q", name, entry["status"], "ok")
		}
		if _, timed := entry["duration_ms"]; !timed {
			t.Errorf("checks[%q] has no duration", name)
		}
	}
}

// An unready instance has to say which dependency is at fault; a bare 503
// leaves whoever is paged guessing.
func TestReadyHandler_NamesTheFailingDependency(t *testing.T) {
	failing := func(context.Context) error { return errors.New("connection refused") }

	status, body := call(t, probeApp(t,
		Check{Name: "postgres", Probe: failing},
		Check{Name: "cache", Probe: ok},
	), "/readyz")

	if status != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if body["status"] != "unavailable" {
		t.Errorf("status field = %v, want %q", body["status"], "unavailable")
	}

	checks := body["checks"].(map[string]any)

	postgres := checks["postgres"].(map[string]any)
	if postgres["status"] != "unavailable" {
		t.Errorf("postgres.status = %v, want %q", postgres["status"], "unavailable")
	}
	if _, reported := postgres["error"]; reported {
		t.Error("the failing check published a reason; /readyz is unauthenticated")
	}

	cache := checks["cache"].(map[string]any)
	if cache["status"] != "ok" {
		t.Errorf("cache.status = %v, want %q - a healthy dependency stays healthy", cache["status"], "ok")
	}
	if _, reported := cache["error"]; reported {
		t.Error("a healthy check should carry no error field")
	}
}

func TestReadyHandler_StopsAProbeThatOverrunsItsBudget(t *testing.T) {
	blocking := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	app := fiber.New(fiber.Config{ErrorHandler: NewErrorHandler(silentLogger())})
	app.Get("/readyz", ReadyHandler(silentLogger(), 20*time.Millisecond, Check{Name: "postgres", Probe: blocking}))

	start := time.Now()
	status, body := call(t, app, "/readyz")
	elapsed := time.Since(start)

	if status != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if elapsed > time.Second {
		t.Errorf("the probe ran for %s, so the timeout did not stop it", elapsed)
	}

	postgres := body["checks"].(map[string]any)["postgres"].(map[string]any)
	if postgres["status"] != "unavailable" {
		t.Errorf("postgres.status = %v, want %q", postgres["status"], "unavailable")
	}
}

func TestReadyHandler_KeepsTheReasonOutOfTheResponse(t *testing.T) {
	leak := "failed to connect to `user=todo_user database=to_do_prod`: " +
		"db.internal.example:5432: connection refused"

	app, logs := probeAppWithLogs(t, Check{
		Name:  "postgres",
		Probe: func(context.Context) error { return errors.New(leak) },
	})

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/readyz", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}

	for _, secret := range []string{"todo_user", "to_do_prod", "db.internal.example", "5432"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the response leaked %q: %s", secret, raw)
		}
	}
	if !strings.Contains(string(raw), "postgres") {
		t.Errorf("the response does not name the failing check: %s", raw)
	}

	// The operator still needs the reason, so it has to be in the log.
	if !strings.Contains(logs.String(), "db.internal.example") {
		t.Errorf("the reason reached neither the client nor the log: %s", logs.String())
	}
}
