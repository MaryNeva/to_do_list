package middleware

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/logger"
)

type ctxKey string

const marker ctxKey = "set-by-an-earlier-middleware"

func requestContextApp(t *testing.T, base context.Context, before fiber.Handler, handler fiber.Handler) *fiber.App {
	t.Helper()

	app := fiber.New()
	if before != nil {
		app.Use(before)
	}
	app.Use(RequestContext(base))
	app.Get("/", handler)
	return app
}

func get(t *testing.T, app *fiber.App) {
	t.Helper()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("app.Test(): %v", err)
	}
	_ = resp.Body.Close()
}

// The reason the middleware exists: a handler's context has to end when the
// server's does, and Fiber's own user context never ends at all. The check
// happens inside the handler, because the request context is - rightly -
// cancelled again the moment the request is over.
func TestRequestContext_RequestsEndWithTheBaseContext(t *testing.T) {
	base, shutdown := context.WithCancel(context.Background())

	var (
		ran      bool
		startErr error
		reached  bool
	)
	app := requestContextApp(t, base, nil, func(c *fiber.Ctx) error {
		ran = true
		ctx := c.UserContext()
		startErr = ctx.Err()

		// The shutdown happens while this request is still running, which is
		// the only moment the propagation matters.
		shutdown()

		select {
		case <-ctx.Done():
			reached = true
		case <-time.After(time.Second):
		}
		return c.SendStatus(fiber.StatusOK)
	})

	get(t, app)

	if !ran {
		t.Fatal("the handler never ran")
	}
	if startErr != nil {
		t.Fatalf("the request context was already done when the handler started: %v", startErr)
	}
	if !reached {
		t.Fatal("cancelling the base context did not reach the request; in-flight work would not stop")
	}
}

// Replacing the user context outright would make this middleware's position
// in the chain an invisible rule: mount anything that stores a value before
// it, and the value disappears with no error anywhere.
func TestRequestContext_KeepsWhatAnEarlierMiddlewareStored(t *testing.T) {
	earlier := func(c *fiber.Ctx) error {
		c.SetUserContext(context.WithValue(c.UserContext(), marker, "still here"))
		return c.Next()
	}

	var got any
	app := requestContextApp(t, context.Background(), earlier, func(c *fiber.Ctx) error {
		got = c.UserContext().Value(marker)
		return c.SendStatus(fiber.StatusOK)
	})

	get(t, app)

	if got != "still here" {
		t.Errorf("the value stored before this middleware reached the handler as %v, want %q", got, "still here")
	}
}

// Values are inherited; cancellation is not. A predecessor that hands over a
// context which is already done must not end a request that has only just
// started - the request's lifetime is this middleware's to decide.
func TestRequestContext_DoesNotInheritAPredecessorsCancellation(t *testing.T) {
	earlier := func(c *fiber.Ctx) error {
		dead, cancel := context.WithCancel(c.UserContext())
		cancel()
		c.SetUserContext(context.WithValue(dead, marker, "still here"))
		return c.Next()
	}

	var (
		err   error
		value any
	)
	app := requestContextApp(t, context.Background(), earlier, func(c *fiber.Ctx) error {
		err, value = c.UserContext().Err(), c.UserContext().Value(marker)
		return c.SendStatus(fiber.StatusOK)
	})

	get(t, app)

	if err != nil {
		t.Errorf("the request context arrived cancelled: %v", err)
	}
	if value != "still here" {
		t.Errorf("the inherited value did not survive: %v", value)
	}
}

// The request id is put where both the logger and the handlers look for it.
func TestRequestContext_CarriesTheRequestID(t *testing.T) {
	var (
		fromContext string
		fromLocals  any
	)
	app := requestContextApp(t, context.Background(), nil, func(c *fiber.Ctx) error {
		fromContext = logger.RequestID(c.UserContext())
		fromLocals = c.Locals(logger.RequestIDKey)
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(fiber.MethodGet, "/", nil)
	req.Header.Set(fiber.HeaderXRequestID, "abc-123")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test(): %v", err)
	}
	_ = resp.Body.Close()

	if fromContext != "abc-123" {
		t.Errorf("request id in the context = %q, want %q", fromContext, "abc-123")
	}
	if fromLocals != "abc-123" {
		t.Errorf("request id in the locals = %v, want %q", fromLocals, "abc-123")
	}
}

// A nil base is a programming mistake this middleware absorbs rather than
// panicking on in the first request of a deployment.
func TestRequestContext_ToleratesANilBase(t *testing.T) {
	var (
		ran bool
		err error
	)
	app := requestContextApp(t, nil, nil, func(c *fiber.Ctx) error {
		ran, err = true, c.UserContext().Err()
		return c.SendStatus(fiber.StatusOK)
	})

	get(t, app)

	if !ran {
		t.Fatal("the handler never ran")
	}
	if err != nil {
		t.Errorf("the request context was already done: %v", err)
	}
}
