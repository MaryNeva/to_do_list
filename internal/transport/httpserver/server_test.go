package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
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

func (stubTasks) Create(context.Context, domain.Claims, string, string) (domain.Task, error) {
	return domain.Task{}, apperr.ErrValidation
}
func (stubTasks) Get(context.Context, domain.Claims, int64) (domain.Task, error) {
	return domain.Task{}, apperr.ErrNotFound
}
func (stubTasks) List(context.Context, domain.Claims, domain.TaskFilter) (domain.Page[domain.Task], error) {
	return domain.Page[domain.Task]{}, nil
}
func (stubTasks) Update(context.Context, domain.Claims, int64, domain.TaskUpdate) (domain.Task, error) {
	return domain.Task{}, apperr.ErrNotFound
}
func (stubTasks) Delete(context.Context, domain.Claims, int64) error { return apperr.ErrNotFound }
func (stubTasks) ToggleStatus(context.Context, domain.Claims, int64) (domain.Task, error) {
	return domain.Task{}, apperr.ErrNotFound
}

type stubUsers struct{}

func (stubUsers) Get(context.Context, domain.Claims, int64) (domain.User, error) {
	return domain.User{}, apperr.ErrNotFound
}
func (stubUsers) List(context.Context, domain.Claims, domain.PageRequest) (domain.Page[domain.User], error) {
	return domain.Page[domain.User]{}, nil
}
func (stubUsers) Update(context.Context, domain.Claims, int64, string, string, string) (domain.User, error) {
	return domain.User{}, apperr.ErrNotFound
}
func (stubUsers) Delete(context.Context, domain.Claims, int64) error { return apperr.ErrNotFound }

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

func assembledApp(t *testing.T, auth AuthService) (*fiber.App, *countingRecorder, *bytes.Buffer) {
	t.Helper()

	recorder := &countingRecorder{}
	var logs bytes.Buffer

	app := New(
		context.Background(),
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

type acceptingAuth struct{ panickingAuth }

func (acceptingAuth) ValidateToken(context.Context, string) (domain.Claims, error) {
	return domain.Claims{UserID: 7}, nil
}

type blockingUsers struct {
	stubUsers
	entered chan struct{}
	seen    chan error
}

func (u blockingUsers) Get(ctx context.Context, _ domain.Claims, _ int64) (domain.User, error) {
	close(u.entered)
	// The timeout keeps a regression from wedging the suite: without
	// cancellation this would otherwise block shutdown for ever.
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
	}
	u.seen <- ctx.Err()
	return domain.User{}, ctx.Err()
}

// Cancelling the context the server was built with has to reach work already
// running. Fiber's UserContext starts as context.Background, so this only
// holds because the app derives request contexts from that base; app.Test
// cannot show it, because fasthttp ties cancellation to a real server.
func TestApp_ShutdownCancelsWorkInFlight(t *testing.T) {
	base, shutdown := context.WithCancel(context.Background())
	defer shutdown()

	users := blockingUsers{entered: make(chan struct{}), seen: make(chan error, 1)}

	app := New(
		base,
		Config{
			AppName: "to-do-list-test", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
			CORSAllowOrigins: "*", CORSAllowMethods: "GET", CORSAllowHeaders: "Content-Type",
			RateLimitAuthMaxRequests: 100, RateLimitAuthWindow: time.Minute,
			HealthReadyTimeout: time.Second, MetricsPath: "/metrics",
		},
		logger.New(io.Discard, "error", "json"),
		Observability{Build: buildinfo.Info{Version: "test"}},
		acceptingAuth{},
		stubTasks{},
		users,
	)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go app.Listener(ln)
	defer app.Shutdown()

	go func() {
		req, _ := http.NewRequest(fiber.MethodGet, "http://"+ln.Addr().String()+"/api/v1/users/7", nil)
		req.Header.Set(fiber.HeaderAuthorization, "Bearer token")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()

	select {
	case <-users.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the service")
	}

	shutdown()

	select {
	case err := <-users.seen:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("service saw %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel the work already running")
	}
}

// The rate limiter answers on its own, before any handler, and used to write
// a bare "Too Many Requests" - the one response a client could not read the
// way it reads every other failure.
func TestApp_RateLimitedRequestCarriesTheErrorEnvelope(t *testing.T) {
	app := New(
		context.Background(),
		Config{
			AppName: "to-do-list-test", ReadTimeout: time.Second, WriteTimeout: time.Second,
			CORSAllowOrigins: "*", CORSAllowMethods: "POST", CORSAllowHeaders: "Content-Type",
			RateLimitAuthMaxRequests: 1, RateLimitAuthWindow: time.Minute,
			HealthReadyTimeout: time.Second, MetricsPath: "/metrics",
		},
		logger.New(io.Discard, "error", "json"),
		Observability{Build: buildinfo.Info{Version: "test"}},
		panickingAuth{},
		stubTasks{},
		stubUsers{},
	)

	var resp *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(fiber.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"mary","password":"secret123"}`))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

		var err error
		resp, err = app.Test(req)
		if err != nil {
			t.Fatalf("request %d: %v", attempt, err)
		}
	}

	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusTooManyRequests)
	}

	raw, _ := io.ReadAll(resp.Body)
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("the 429 body is not JSON: %v (%s)", err, raw)
	}
	if envelope.Code != "rate_limited" {
		t.Errorf("code = %q, want %q (%s)", envelope.Code, "rate_limited", raw)
	}
	if envelope.Message == "" {
		t.Errorf("the 429 body carries no message: %s", raw)
	}
}
