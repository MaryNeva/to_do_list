package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
	"to-do-list/internal/buildinfo"
	"to-do-list/internal/domain"
	"to-do-list/internal/logger"
)

type panickingAuth struct {
	panicOnLogin bool
}

func (a panickingAuth) Register(context.Context, string, string, string) (domain.User, error) {
	return domain.User{}, apperr.ErrValidation
}

func (a panickingAuth) Login(context.Context, string, string) (domain.Tokens, domain.User, error) {
	if a.panicOnLogin {
		panic("the use case exploded")
	}
	return domain.Tokens{AccessToken: "t", AccessExpiresAt: time.Now().Add(time.Hour)}, domain.User{ID: 1, Username: "mary"}, nil
}

func (a panickingAuth) Refresh(context.Context, string) (domain.Tokens, error) {
	return domain.Tokens{}, apperr.ErrUnauthorized
}

func (a panickingAuth) Logout(context.Context, string) error { return nil }

func (a panickingAuth) ValidateToken(context.Context, string) (domain.Claims, error) {
	return domain.Claims{}, apperr.ErrUnauthorized
}

type stubTasks struct{}

func (stubTasks) Create(context.Context, int64, string, string) (domain.Task, error) {
	return domain.Task{}, apperr.ErrValidation
}
func (stubTasks) Get(context.Context, int64, int64) (domain.Task, error) {
	return domain.Task{}, apperr.ErrNotFound
}
func (stubTasks) List(context.Context, int64, domain.TaskFilter) (domain.Page[domain.Task], error) {
	return domain.Page[domain.Task]{}, nil
}
func (stubTasks) Update(context.Context, int64, int64, string, string) (domain.Task, error) {
	return domain.Task{}, apperr.ErrNotFound
}
func (stubTasks) Delete(context.Context, int64, int64) error { return apperr.ErrNotFound }
func (stubTasks) ToggleStatus(context.Context, int64, int64) (domain.Task, error) {
	return domain.Task{}, apperr.ErrNotFound
}

type stubUsers struct{}

func (stubUsers) Get(context.Context, int64) (domain.User, error) {
	return domain.User{}, apperr.ErrNotFound
}
func (stubUsers) List(context.Context, domain.PageRequest) (domain.Page[domain.User], error) {
	return domain.Page[domain.User]{}, nil
}
func (stubUsers) Update(context.Context, int64, string, string, string) (domain.User, error) {
	return domain.User{}, apperr.ErrNotFound
}
func (stubUsers) Delete(context.Context, int64) error { return apperr.ErrNotFound }

type countingRecorder struct {
	mu sync.Mutex

	requests []recordedRequest
	inFlight int
	peak     int
}

type recordedRequest struct {
	method string
	route  string
	status int
}

func (r *countingRecorder) ObserveRequest(method, route string, status int, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, recordedRequest{method, route, status})
}

func (r *countingRecorder) IncInFlight() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight++
	if r.inFlight > r.peak {
		r.peak = r.inFlight
	}
}

func (r *countingRecorder) DecInFlight() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight--
}

func (r *countingRecorder) snapshot() ([]recordedRequest, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedRequest(nil), r.requests...), r.inFlight, r.peak
}

func assembledApp(t *testing.T, auth domain.AuthService) (*fiber.App, *countingRecorder, *bytes.Buffer) {
	t.Helper()

	recorder := &countingRecorder{}
	var logs bytes.Buffer

	app := New(
		Config{
			AppName:                  "to-do-list-test",
			ReadTimeout:              time.Second,
			WriteTimeout:             time.Second,
			CORSAllowOrigins:         "*",
			CORSAllowMethods:         "GET, POST",
			CORSAllowHeaders:         "Content-Type",
			RateLimitAuthMaxRequests: 100,
			RateLimitAuthWindow:      time.Minute,
			HealthReadyTimeout:       time.Second,
			MetricsEnabled:           false,
			MetricsPath:              "/metrics",
		},
		logger.New(&logs, "info", "json"),
		Observability{Requests: recorder, Build: buildinfo.Info{Version: "test"}},
		auth,
		stubTasks{},
		stubUsers{},
	)

	return app, recorder, &logs
}

func login(t *testing.T, app *fiber.App) (status int, body []byte) {
	t.Helper()

	req := httptest.NewRequest(fiber.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"mary","password":"password123"}`))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, raw
}

func requestLines(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()

	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if raw == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			t.Fatalf("log line is not JSON: %v (%q)", err, raw)
		}
		if record["msg"] == "http_request" {
			lines = append(lines, record)
		}
	}
	return lines
}

func TestApp_PanicIsAccountedAsA500(t *testing.T) {
	app, recorder, logs := assembledApp(t, panickingAuth{panicOnLogin: true})

	status, body := login(t, app)

	if status != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if strings.Contains(string(body), "exploded") {
		t.Errorf("the panic message reached the client: %s", body)
	}

	requests, inFlight, peak := recorder.snapshot()

	if len(requests) != 1 {
		t.Fatalf("recorded %d requests, want exactly 1", len(requests))
	}
	if requests[0].status != fiber.StatusInternalServerError {
		t.Errorf("recorded status = %d, want 500", requests[0].status)
	}
	if requests[0].route != "/api/v1/auth/login" {
		t.Errorf("recorded route = %q, want %q", requests[0].route, "/api/v1/auth/login")
	}
	if inFlight != 0 {
		t.Errorf("in-flight = %d after the request finished, want 0", inFlight)
	}
	if peak != 1 {
		t.Errorf("in-flight peak = %d, want 1", peak)
	}

	lines := requestLines(t, logs)
	if len(lines) != 1 {
		t.Fatalf("wrote %d request log lines, want exactly 1", len(lines))
	}
	if got := lines[0]["status"]; got != float64(fiber.StatusInternalServerError) {
		t.Errorf("logged status = %v, want 500", got)
	}
	if got := lines[0]["level"]; got != "ERROR" {
		t.Errorf("logged level = %v, want ERROR", got)
	}
	if id, _ := lines[0]["request_id"].(string); id == "" {
		t.Error("the log line carries no request_id")
	}
}

// The ordinary path must keep behaving as before the reorder.
func TestApp_SuccessfulRequestIsStillAccounted(t *testing.T) {
	app, recorder, logs := assembledApp(t, panickingAuth{})

	status, _ := login(t, app)
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	requests, inFlight, _ := recorder.snapshot()
	if len(requests) != 1 || requests[0].status != fiber.StatusOK {
		t.Fatalf("recorded %+v, want one 200", requests)
	}
	if inFlight != 0 {
		t.Errorf("in-flight = %d, want 0", inFlight)
	}

	if lines := requestLines(t, logs); len(lines) != 1 {
		t.Fatalf("wrote %d request log lines, want exactly 1", len(lines))
	}
}

func TestApp_HealthEndpointsAreServed(t *testing.T) {
	app, _, _ := assembledApp(t, panickingAuth{})

	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
	}
}
