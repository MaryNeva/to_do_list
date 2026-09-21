package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
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
func (stubUsers) Update(context.Context, domain.Claims, int64, domain.UserEdit) (domain.User, error) {
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

// listeningApp serves cfg on a real socket: app.Test reports a body-limit
// breach as a transport error instead of a 413 response.
func listeningApp(t *testing.T, cfg Config) string {
	t.Helper()

	app := New(
		context.Background(),
		cfg,
		logger.New(io.Discard, "error", "json"),
		Observability{Build: buildinfo.Info{Version: "test"}},
		panickingAuth{},
		stubTasks{},
		stubUsers{},
	)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = app.Listener(ln)
	}()

	t.Cleanup(func() {
		_ = app.Shutdown()
		<-served
	})

	waitForListener(t, ln.Addr().String())
	return "http://" + ln.Addr().String()
}

func waitForListener(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never started listening", addr)
}

func limitsConfig() Config {
	return Config{
		AppName: "to-do-list-test", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		CORSAllowOrigins: "*", CORSAllowMethods: "GET, POST", CORSAllowHeaders: "Content-Type",
		RateLimitAuthMaxRequests: 100, RateLimitAuthWindow: time.Minute,
		HealthReadyTimeout: time.Second, MetricsPath: "/metrics",
		MaxBodyBytes: 1024,
	}
}

func TestApp_RejectsABodyOverTheLimit(t *testing.T) {
	base := listeningApp(t, limitsConfig())

	oversized := `{"username":"mary","password":"` + strings.Repeat("a", 4096) + `"}`
	resp, err := http.Post(base+"/api/v1/auth/login", fiber.MIMEApplicationJSON, strings.NewReader(oversized))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != fiber.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (%s)", resp.StatusCode, raw)
	}

	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("the 413 body is not JSON: %v (%s)", err, raw)
	}
	if envelope.Code != "payload_too_large" {
		t.Errorf("code = %q, want %q", envelope.Code, "payload_too_large")
	}
}

func TestApp_AcceptsABodyUnderTheLimit(t *testing.T) {
	base := listeningApp(t, limitsConfig())

	resp, err := http.Post(base+"/api/v1/auth/login", fiber.MIMEApplicationJSON,
		strings.NewReader(`{"username":"mary","password":"password123"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != fiber.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, raw)
	}
}

// A forwarding header is trusted only from configured proxies; otherwise any
// caller could get a fresh rate-limit bucket per request.
func TestApp_ForwardedAddressIsOnlyBelievedFromATrustedProxy(t *testing.T) {
	for _, tc := range []struct {
		name           string
		trustedProxies []string
		proxyHeader    string
		wantLastStatus int
	}{
		{
			name:           "no proxy configured: the header is ignored",
			wantLastStatus: fiber.StatusTooManyRequests,
		},
		{
			name:           "the proxy is trusted: each forwarded client gets its own budget",
			trustedProxies: []string{"127.0.0.1"},
			proxyHeader:    fiber.HeaderXForwardedFor,
			wantLastStatus: fiber.StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := limitsConfig()
			cfg.RateLimitAuthMaxRequests = 1
			cfg.TrustedProxies = tc.trustedProxies
			cfg.ProxyHeader = tc.proxyHeader
			base := listeningApp(t, cfg)

			var status int
			for i, client := range []string{"203.0.113.1", "203.0.113.2"} {
				req, err := http.NewRequest(fiber.MethodPost, base+"/api/v1/auth/login",
					strings.NewReader(`{"username":"mary","password":"password123"}`))
				if err != nil {
					t.Fatalf("build request %d: %v", i, err)
				}
				req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
				req.Header.Set(fiber.HeaderXForwardedFor, client)

				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("request %d: %v", i, err)
				}
				status = resp.StatusCode
				resp.Body.Close()
			}

			if status != tc.wantLastStatus {
				t.Errorf("the second client got %d, want %d", status, tc.wantLastStatus)
			}
		})
	}
}

func TestApp_MetricsLeaveThePublicListenerWhenGivenTheirOwnAddress(t *testing.T) {
	exporter := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("todo_build_info 1\n"))
	})

	for _, tc := range []struct {
		name           string
		metricsAddress string
		wantOnPublic   int
	}{
		{name: "no separate address: served next to the API", wantOnPublic: fiber.StatusOK},
		{name: "separate address: absent from the API", metricsAddress: "127.0.0.1:0", wantOnPublic: fiber.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := New(
				context.Background(),
				Config{
					AppName: "to-do-list-test", ReadTimeout: time.Second, WriteTimeout: time.Second,
					CORSAllowOrigins: "*", CORSAllowMethods: "GET", CORSAllowHeaders: "Content-Type",
					RateLimitAuthMaxRequests: 10, RateLimitAuthWindow: time.Minute,
					HealthReadyTimeout: time.Second,
					MetricsEnabled:     true, MetricsPath: "/metrics", MetricsAddress: tc.metricsAddress,
				},
				logger.New(io.Discard, "error", "json"),
				Observability{Exporter: exporter, Build: buildinfo.Info{Version: "test"}},
				panickingAuth{},
				stubTasks{},
				stubUsers{},
			)

			resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/metrics", nil))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantOnPublic {
				t.Errorf("/metrics on the public listener = %d, want %d", resp.StatusCode, tc.wantOnPublic)
			}
		})
	}
}

func TestNewMetricsServer_ServesTheExporterAndNothingElse(t *testing.T) {
	exporter := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("todo_build_info 1\n"))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	_ = ln.Close()

	srv, err := NewMetricsServer(ln.Addr().String(), "/metrics", exporter)
	if err != nil {
		t.Fatalf("NewMetricsServer(): %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.ShutdownWithContext(context.Background()) })

	base := "http://" + srv.Addr()

	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatalf("get /metrics: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "todo_build_info") {
		t.Errorf("/metrics = %d %q, want the exporter's output", resp.StatusCode, raw)
	}

	// The metrics listener must not serve the API.
	resp, err = http.Get(base + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("get /api/v1/tasks: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("/api/v1/tasks on the metrics listener = %d, want 404", resp.StatusCode)
	}
}
