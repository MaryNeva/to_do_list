package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"to-do-list/internal/domain"
	"to-do-list/internal/logger"
	"to-do-list/internal/transport/http/middleware"
	"to-do-list/internal/usecase"
)

type contextProbe struct {
	usecase.UserRepository
	check func(context.Context)
}

func (r contextProbe) GetByID(ctx context.Context, id int64) (domain.User, error) {
	r.check(ctx)
	return domain.User{ID: id}, ctx.Err()
}
func TestUserContextReachesRepository(t *testing.T) {
	type key struct{}
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "cancelled"}[cancelled], func(t *testing.T) {
			deadline := time.Now().Add(time.Hour)
			parent, cancel := context.WithDeadline(context.WithValue(context.Background(), key{}, "value"), deadline)
			defer cancel()
			if cancelled {
				cancel()
			}
			called := false
			repo := contextProbe{check: func(ctx context.Context) {
				called = true
				got, ok := ctx.Deadline()
				if !ok || !got.Equal(deadline) {
					t.Error("parent deadline lost")
				}
				if ctx.Value(key{}) != "value" {
					t.Error("parent value lost")
				}
				if logger.RequestID(ctx) != "request-test" {
					t.Error("request id lost")
				}
				if cancelled && ctx.Err() != context.Canceled {
					t.Error("cancellation lost")
				}
			}}
			uc := usecase.NewUserUseCase(repo, nil, usecase.UserConfig{Timeout: 2 * time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			app := fiber.New()
			app.Use(requestid.New(), middleware.RequestContext(parent), middleware.Auth(fakeAuthValidator{claims: domain.Claims{UserID: 1}}))
			NewUserHandler(uc).Mount(app.Group("/users"))
			req := httptest.NewRequest("GET", "/users/1", nil)
			req.Header.Set("Authorization", "Bearer token")
			req.Header.Set("X-Request-ID", "request-test")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if !called {
				t.Fatal("repository was not called")
			}
			if !cancelled && resp.StatusCode != 200 {
				t.Fatalf("status %d", resp.StatusCode)
			}
		})
	}
}
