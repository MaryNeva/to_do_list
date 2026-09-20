package middleware

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
)

type observation struct {
	method string
	route  string
	status int
}

type recordingMetrics struct {
	mu sync.Mutex

	observed []observation
	inFlight int
	peak     int
}

func (r *recordingMetrics) ObserveRequest(method, route string, status int, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observed = append(r.observed, observation{method, route, status})
}

func (r *recordingMetrics) IncInFlight() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight++
	if r.inFlight > r.peak {
		r.peak = r.inFlight
	}
}

func (r *recordingMetrics) DecInFlight() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight--
}

func (r *recordingMetrics) snapshot() ([]observation, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observation(nil), r.observed...), r.inFlight, r.peak
}

func metricsApp(rec RequestRecorder) *fiber.App {
	app := fiber.New()
	app.Use(Metrics(rec, "/metrics"))
	app.Get("/metrics", func(c *fiber.Ctx) error { return c.SendString("# exposition") })
	app.Get("/tasks/:id", func(c *fiber.Ctx) error { return c.SendString("task") })
	app.Get("/boom", func(c *fiber.Ctx) error { return fiber.ErrTeapot })
	app.Get("/tasks/:id/missing", func(c *fiber.Ctx) error { return apperr.ErrNotFound })
	return app
}

func request(t *testing.T, app *fiber.App, path string) {
	t.Helper()
	if _, err := app.Test(httptest.NewRequest(fiber.MethodGet, path, nil)); err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
}

func TestMetrics_LabelsTheRouteTemplateNotThePath(t *testing.T) {
	rec := &recordingMetrics{}
	app := metricsApp(rec)

	request(t, app, "/tasks/1")
	request(t, app, "/tasks/2")
	request(t, app, "/tasks/3")

	observed, _, _ := rec.snapshot()
	if len(observed) != 3 {
		t.Fatalf("recorded %d requests, want 3", len(observed))
	}
	for _, o := range observed {
		if o.route != "/tasks/:id" {
			t.Errorf("route label = %q, want %q", o.route, "/tasks/:id")
		}
		if o.method != fiber.MethodGet || o.status != fiber.StatusOK {
			t.Errorf("observation = %+v, want GET/200", o)
		}
	}
}

func TestMetrics_UnmatchedRequestsShareOneLabel(t *testing.T) {
	rec := &recordingMetrics{}
	app := metricsApp(rec)

	request(t, app, "/../etc/passwd")
	request(t, app, "/wp-login.php")

	observed, _, _ := rec.snapshot()
	if len(observed) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(observed))
	}
	for _, o := range observed {
		if o.route != routeUnmatched {
			t.Errorf("route label = %q, want %q", o.route, routeUnmatched)
		}
		if o.status != fiber.StatusNotFound {
			t.Errorf("status = %d, want 404", o.status)
		}
	}
}

func TestMetrics_RecordsTheStatusOfAFailedRequest(t *testing.T) {
	rec := &recordingMetrics{}
	app := metricsApp(rec)

	request(t, app, "/boom")

	observed, _, _ := rec.snapshot()
	if len(observed) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(observed))
	}
	if observed[0].status != fiber.StatusTeapot {
		t.Errorf("status = %d, want %d", observed[0].status, fiber.StatusTeapot)
	}
}

func TestMetrics_SkipsItsOwnExpositionEndpoint(t *testing.T) {
	rec := &recordingMetrics{}
	app := metricsApp(rec)

	request(t, app, "/metrics")

	observed, _, peak := rec.snapshot()
	if len(observed) != 0 {
		t.Errorf("the scrape endpoint should not observe itself, got %+v", observed)
	}
	if peak != 0 {
		t.Errorf("in-flight peak = %d, want 0", peak)
	}
}

func TestMetrics_InFlightReturnsToZero(t *testing.T) {
	rec := &recordingMetrics{}
	app := metricsApp(rec)

	request(t, app, "/tasks/1")
	request(t, app, "/boom")

	_, inFlight, peak := rec.snapshot()
	if inFlight != 0 {
		t.Errorf("in-flight = %d after all requests finished, want 0", inFlight)
	}
	if peak != 1 {
		t.Errorf("in-flight peak = %d, want 1", peak)
	}
}

func TestMetrics_HandlerNotFoundKeepsItsRouteTemplate(t *testing.T) {
	rec := &recordingMetrics{}
	app := metricsApp(rec)

	request(t, app, "/tasks/42/missing")

	observed, _, _ := rec.snapshot()
	if len(observed) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(observed))
	}
	if observed[0].route != "/tasks/:id/missing" {
		t.Errorf("route label = %q, want %q", observed[0].route, "/tasks/:id/missing")
	}
	if observed[0].status != fiber.StatusNotFound {
		t.Errorf("status = %d, want 404", observed[0].status)
	}
}

func TestMetrics_MethodLabelIsACopyFromAClosedSet(t *testing.T) {
	rec := &recordingMetrics{}
	app := fiber.New()
	app.Use(Metrics(rec))
	app.All("/thing", func(c *fiber.Ctx) error { return c.SendString("ok") })

	methods := []string{
		fiber.MethodGet, fiber.MethodPost, fiber.MethodPut,
		fiber.MethodPatch, fiber.MethodDelete, fiber.MethodHead, fiber.MethodOptions,
	}
	for _, m := range methods {
		if _, err := app.Test(httptest.NewRequest(m, "/thing", nil)); err != nil {
			t.Fatalf("%s /thing: %v", m, err)
		}
	}

	known := map[string]bool{}
	for _, m := range methods {
		known[m] = true
	}

	observed, _, _ := rec.snapshot()
	if len(observed) != len(methods) {
		t.Fatalf("recorded %d requests, want %d", len(observed), len(methods))
	}
	for _, o := range observed {
		if !known[o.method] {
			t.Errorf("method label = %q, which is not a known method", o.method)
		}

		if unsafeSameBacking(o.method) {
			t.Errorf("method label %q still points at the request buffer", o.method)
		}
	}
}

func unsafeSameBacking(s string) bool {
	for _, m := range []string{
		fiber.MethodGet, fiber.MethodPost, fiber.MethodPut,
		fiber.MethodPatch, fiber.MethodDelete, fiber.MethodHead,
		fiber.MethodOptions, methodOther,
	} {
		if s == m && unsafe.StringData(s) == unsafe.StringData(m) {
			return false
		}
	}
	return true
}

func TestMetrics_UnknownMethodCollapsesIntoOneLabel(t *testing.T) {
	rec := &recordingMetrics{}
	app := fiber.New()
	app.Use(Metrics(rec))
	app.All("/thing", func(c *fiber.Ctx) error { return c.SendString("ok") })

	for _, m := range []string{"PROPFIND", "MKCOL", "BREW"} {
		if _, err := app.Test(httptest.NewRequest(m, "/thing", nil)); err != nil {
			continue
		}
	}

	observed, _, _ := rec.snapshot()
	for _, o := range observed {
		if o.method != methodOther {
			t.Errorf("method label = %q, want %q", o.method, methodOther)
		}
	}
}
