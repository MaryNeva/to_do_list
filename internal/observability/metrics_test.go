package observability

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"to-do-list/internal/buildinfo"
)

func newTestMetrics() *Metrics {
	return New("todo", buildinfo.Info{
		Version:   "1.2.3",
		Commit:    "abc123",
		BuiltAt:   "2026-01-01T00:00:00Z",
		GoVersion: "go1.24.0",
	})
}

func TestNew_TwoRegistriesDoNotCollide(t *testing.T) {
	newTestMetrics()
	newTestMetrics()
}

func TestObserveRequest_CountsByMethodRouteAndStatus(t *testing.T) {
	m := newTestMetrics()

	m.ObserveRequest("GET", "/api/v1/tasks/:id", 200, 12*time.Millisecond)
	m.ObserveRequest("GET", "/api/v1/tasks/:id", 200, 8*time.Millisecond)
	m.ObserveRequest("GET", "/api/v1/tasks/:id", 404, 3*time.Millisecond)

	expected := `
# HELP todo_http_requests_total Requests served, by method, route template and status code.
# TYPE todo_http_requests_total counter
todo_http_requests_total{method="GET",route="/api/v1/tasks/:id",status="200"} 2
todo_http_requests_total{method="GET",route="/api/v1/tasks/:id",status="404"} 1
`

	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(expected), "todo_http_requests_total"); err != nil {
		t.Error(err)
	}

	if got := testutil.CollectAndCount(m.httpDuration); got != 1 {
		t.Errorf("duration series = %d, want 1 (one per method/route pair)", got)
	}
}

func TestInFlight_RisesAndFalls(t *testing.T) {
	m := newTestMetrics()

	m.IncInFlight()
	m.IncInFlight()
	m.DecInFlight()

	if got := testutil.ToFloat64(m.httpInFlight); got != 1 {
		t.Errorf("in-flight = %v, want 1", got)
	}
}

func TestBusinessCounters_AreLabelledByOutcome(t *testing.T) {
	m := newTestMetrics()

	m.AuthAttempt("login", "success")
	m.AuthAttempt("login", "rejected")
	m.AuthAttempt("login", "rejected")
	m.RefreshRotation("reuse")
	m.SessionsRevoked("token_reuse", 1)

	expected := `
# HELP todo_auth_attempts_total Authentication operations, by operation and outcome.
# TYPE todo_auth_attempts_total counter
todo_auth_attempts_total{operation="login",outcome="rejected"} 2
todo_auth_attempts_total{operation="login",outcome="success"} 1
`

	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(expected), "todo_auth_attempts_total"); err != nil {
		t.Error(err)
	}

	if got := testutil.ToFloat64(m.refreshRotations.WithLabelValues("reuse")); got != 1 {
		t.Errorf("reuse rotations = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.sessionsRevoked.WithLabelValues("token_reuse")); got != 1 {
		t.Errorf("sessions revoked = %v, want 1", got)
	}
}

func TestCleanupRun_RecordsOutcomeAndVolume(t *testing.T) {
	m := newTestMetrics()

	m.CleanupRun("success", 17, 5*time.Millisecond)
	m.CleanupRun("failure", 0, time.Millisecond)

	if got := testutil.ToFloat64(m.cleanupRemoved); got != 17 {
		t.Errorf("rows removed = %v, want 17", got)
	}
	if got := testutil.ToFloat64(m.cleanupRuns.WithLabelValues("failure")); got != 1 {
		t.Errorf("failed sweeps = %v, want 1", got)
	}
}

func TestBuildInfo_IsExposedAsLabels(t *testing.T) {
	m := newTestMetrics()

	expected := `
# HELP todo_build_info Build identity of the running binary; the value is always 1.
# TYPE todo_build_info gauge
todo_build_info{built_at="2026-01-01T00:00:00Z",commit="abc123",go_version="go1.24.0",version="1.2.3"} 1
`

	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(expected), "todo_build_info"); err != nil {
		t.Error(err)
	}
}

func TestHandler_RendersTheExpositionFormat(t *testing.T) {
	m := newTestMetrics()
	m.ObserveRequest("POST", "/api/v1/auth/login", 200, time.Millisecond)

	if got := testutil.CollectAndCount(m.httpRequests); got != 1 {
		t.Fatalf("request series = %d, want 1", got)
	}
	if m.Handler() == nil {
		t.Error("Handler() returned nil")
	}
}

func TestPreload_MakesKnownSeriesStartAtZero(t *testing.T) {
	m := newTestMetrics()

	if got := testutil.CollectAndCount(m.cleanupRuns); got != 0 {
		t.Fatalf("cleanup series before preloading = %d, want 0", got)
	}

	m.Preload(KnownLabels{
		AuthOperations:    []string{"login", "register"},
		AuthOutcomes:      []string{"success", "rejected", "failure"},
		RotationOutcomes:  []string{"success", "reuse"},
		RevocationReasons: []string{"logout"},
		CleanupOutcomes:   []string{"success", "failure"},
	})

	if got := testutil.CollectAndCount(m.authAttempts); got != 6 {
		t.Errorf("auth series = %d, want 6 (2 operations x 3 outcomes)", got)
	}
	if got := testutil.ToFloat64(m.cleanupRuns.WithLabelValues("failure")); got != 0 {
		t.Errorf("preloaded counter = %v, want 0", got)
	}

	// Preloaded series start at zero.
	if got := testutil.ToFloat64(m.authAttempts.WithLabelValues("login", "success")); got != 0 {
		t.Errorf("preloaded counter = %v, want 0", got)
	}

	m.AuthAttempt("login", "success")
	if got := testutil.ToFloat64(m.authAttempts.WithLabelValues("login", "success")); got != 1 {
		t.Errorf("after one attempt = %v, want 1", got)
	}
}
