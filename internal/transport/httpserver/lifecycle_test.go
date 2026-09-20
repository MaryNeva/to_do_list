package httpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/buildinfo"
	"to-do-list/internal/domain"
	"to-do-list/internal/logger"
	"to-do-list/internal/transport/http/handler"
)

// slowUsers answers after work that takes a while, and records whether its
// context was cancelled while it was working.
type slowUsers struct {
	stubUsers
	entered chan struct{}
	work    time.Duration
	seen    chan error

	once sync.Once
}

func (u *slowUsers) Get(ctx context.Context, _ domain.Claims, _ int64) (domain.User, error) {
	u.once.Do(func() { close(u.entered) })

	select {
	case <-time.After(u.work):
	case <-ctx.Done():
	}

	u.seen <- ctx.Err()
	if ctx.Err() != nil {
		return domain.User{}, ctx.Err()
	}
	return domain.User{ID: 7, Username: "mary", Email: "mary@example.com"}, nil
}

// blockedUsers never finishes on its own; only cancellation releases it.
type blockedUsers struct {
	stubUsers
	entered chan struct{}
	seen    chan error

	once sync.Once
}

func (u *blockedUsers) Get(ctx context.Context, _ domain.Claims, _ int64) (domain.User, error) {
	u.once.Do(func() { close(u.entered) })

	select {
	case <-ctx.Done():
	case <-time.After(30 * time.Second): // escape hatch: a regression must not wedge the suite
	}

	u.seen <- ctx.Err()
	return domain.User{}, ctx.Err()
}

func servingApp(t *testing.T, requestCtx context.Context, users handler.UserService) (*fiber.App, string) {
	t.Helper()

	app := New(
		requestCtx,
		Config{
			AppName: "to-do-list-test", ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second,
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
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = app.Listener(ln) }()
	waitForListener(t, ln.Addr().String())

	return app, "http://" + ln.Addr().String()
}

func getUser(base string) (*http.Response, error) {
	req, err := http.NewRequest(fiber.MethodGet, base+"/api/v1/users/7", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")
	return http.DefaultClient.Do(req)
}

// This is the whole difference between a graceful shutdown and a kill: a
// request that was already running when the signal arrived gets to finish.
// It used to be cancelled, because request contexts hung off the same
// context that signal.NotifyContext cancels.
func TestDrain_LetsAnInFlightRequestFinish(t *testing.T) {
	requestCtx, abandonInFlight := context.WithCancel(context.Background())
	defer abandonInFlight()

	users := &slowUsers{entered: make(chan struct{}), work: 300 * time.Millisecond, seen: make(chan error, 1)}
	app, base := servingApp(t, requestCtx, users)

	type result struct {
		status int
		err    error
	}
	answered := make(chan result, 1)
	go func() {
		resp, err := getUser(base)
		if err != nil {
			answered <- result{err: err}
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		answered <- result{status: resp.StatusCode}
	}()

	select {
	case <-users.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the service")
	}

	// The signal arrives here, with the request half-done.
	if err := Drain(5*time.Second, abandonInFlight, app, nil); err != nil {
		t.Fatalf("Drain(): %v", err)
	}

	select {
	case err := <-users.seen:
		if err != nil {
			t.Errorf("the handler was cancelled mid-request: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never finished")
	}

	select {
	case got := <-answered:
		if got.err != nil {
			t.Fatalf("the client never got an answer: %v", got.err)
		}
		if got.status != fiber.StatusOK {
			t.Errorf("status = %d, want 200 - the request started before the shutdown", got.status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the client never got an answer")
	}
}

// The grace period is a bound, not a promise: work still running when it
// expires is cancelled rather than waited on for ever.
func TestDrain_CancelsWorkThatOutlivesTheGracePeriod(t *testing.T) {
	requestCtx, abandonInFlight := context.WithCancel(context.Background())
	defer abandonInFlight()

	users := &blockedUsers{entered: make(chan struct{}), seen: make(chan error, 1)}
	app, base := servingApp(t, requestCtx, users)

	go func() {
		if resp, err := getUser(base); err == nil {
			resp.Body.Close()
		}
	}()

	select {
	case <-users.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the service")
	}

	start := time.Now()
	err := Drain(300*time.Millisecond, abandonInFlight, app, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("Drain() reported success although a request was still running")
	}
	if elapsed > 3*time.Second {
		t.Errorf("Drain() took %s; the grace period was 300ms", elapsed)
	}

	select {
	case seen := <-users.seen:
		if !errors.Is(seen, context.Canceled) {
			t.Errorf("the handler saw %v, want context.Canceled once the grace period ran out", seen)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler was never released")
	}
}

// Before, the metrics listener was drained first and out of the same budget,
// so a slow scrape could spend the time the API needed.
func TestDrain_GivesEveryServerTheSameBudgetAtTheSameTime(t *testing.T) {
	requestCtx, abandonInFlight := context.WithCancel(context.Background())
	defer abandonInFlight()

	users := &blockedUsers{entered: make(chan struct{}), seen: make(chan error, 1)}
	app, base := servingApp(t, requestCtx, users)

	exporter := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("todo_build_info 1\n"))
	})
	metrics, err := NewMetricsServer("127.0.0.1:0", "/metrics", exporter)
	if err != nil {
		t.Fatalf("NewMetricsServer(): %v", err)
	}
	go func() { _ = metrics.Serve() }()

	if resp, err := http.Get("http://" + metrics.Addr() + "/metrics"); err != nil {
		t.Fatalf("the metrics listener never served: %v", err)
	} else {
		resp.Body.Close()
	}

	go func() {
		if resp, err := getUser(base); err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-users.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the service")
	}

	// One budget, spent once: two sequential drains would take twice this.
	const grace = 400 * time.Millisecond
	start := time.Now()
	_ = Drain(grace, abandonInFlight, app, metrics)
	elapsed := time.Since(start)

	if elapsed > 2*grace {
		t.Errorf("Drain() took %s, about twice the %s grace period: the servers drained one after another", elapsed, grace)
	}

	if _, err := http.Get("http://" + metrics.Addr() + "/metrics"); err == nil {
		t.Error("the metrics listener is still answering after Drain()")
	}

	<-users.seen
}

func TestNewMetricsServer_RefusesAPortAlreadyInUse(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer busy.Close()

	exporter := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	// A failure reported later, from a goroutine, would leave a service that
	// passes every health check and exports nothing.
	if _, err := NewMetricsServer(busy.Addr().String(), "/metrics", exporter); err == nil {
		t.Fatal("NewMetricsServer() accepted a port that is already in use")
	}
}
