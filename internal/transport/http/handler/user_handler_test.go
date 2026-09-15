package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
	httptransport "to-do-list/internal/transport/http"
	"to-do-list/internal/transport/http/dto"
	"to-do-list/internal/transport/http/middleware"
)

type fakeUserService struct {
	getFn    func(ctx context.Context, id int64) (domain.User, error)
	listFn   func(ctx context.Context) ([]domain.User, error)
	updateFn func(ctx context.Context, id int64, username, email, newPassword string) (domain.User, error)
	deleteFn func(ctx context.Context, id int64) error
}

func (f fakeUserService) Get(ctx context.Context, id int64) (domain.User, error) {
	return f.getFn(ctx, id)
}
func (f fakeUserService) List(ctx context.Context) ([]domain.User, error) { return f.listFn(ctx) }
func (f fakeUserService) Update(ctx context.Context, id int64, username, email, newPassword string) (domain.User, error) {
	return f.updateFn(ctx, id, username, email, newPassword)
}
func (f fakeUserService) Delete(ctx context.Context, id int64) error { return f.deleteFn(ctx, id) }

func newUserTestApp(svc domain.UserService, claims domain.Claims) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: httptransport.NewErrorHandler(silentTestLogger())})
	protected := app.Group("", middleware.Auth(fakeAuthValidator{claims: claims}))
	NewUserHandler(svc).Mount(protected.Group("/users"))
	return app
}

func TestUserHandler_Get_NeverLeaksPasswordHash(t *testing.T) {
	const secretHash = "$2a$12$thisIsASecretBcryptHashThatMustNeverLeak"

	svc := fakeUserService{
		getFn: func(_ context.Context, id int64) (domain.User, error) {
			return domain.User{
				ID:           id,
				Username:     "alice",
				Email:        "alice@example.com",
				PasswordHash: secretHash,
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
			}, nil
		},
	}
	app := newUserTestApp(svc, domain.Claims{UserID: 1})

	req := httptest.NewRequest(fiber.MethodGet, "/users/1", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read response body: %v", err)
	}
	body := buf.String()

	if strings.Contains(body, secretHash) {
		t.Fatalf("response body leaked the password hash: %s", body)
	}
	if strings.Contains(strings.ToLower(body), "password") {
		t.Fatalf("response body must not mention 'password' at all: %s", body)
	}
}

func TestUserHandler_Get_ForbiddenForOtherUsers(t *testing.T) {
	svc := fakeUserService{
		getFn: func(context.Context, int64) (domain.User, error) {
			t.Fatal("service should not be called when the caller is neither the owner nor an admin")
			return domain.User{}, nil
		},
	}
	app := newUserTestApp(svc, domain.Claims{UserID: 1})

	req := httptest.NewRequest(fiber.MethodGet, "/users/2", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
}

func TestUserHandler_Get_SelfAllowed(t *testing.T) {
	svc := fakeUserService{
		getFn: func(_ context.Context, id int64) (domain.User, error) {
			return domain.User{ID: id, Username: "alice"}, nil
		},
	}
	app := newUserTestApp(svc, domain.Claims{UserID: 1})

	req := httptest.NewRequest(fiber.MethodGet, "/users/1", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
}

func TestUserHandler_Get_AdminCanReadAnyUser(t *testing.T) {
	svc := fakeUserService{
		getFn: func(_ context.Context, id int64) (domain.User, error) {
			return domain.User{ID: id, Username: "someone-else"}, nil
		},
	}
	app := newUserTestApp(svc, domain.Claims{UserID: 0, IsAdmin: true})

	req := httptest.NewRequest(fiber.MethodGet, "/users/2", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
}

func TestUserHandler_List_AdminOnly(t *testing.T) {
	svc := fakeUserService{
		listFn: func(context.Context) ([]domain.User, error) {
			t.Fatal("service should not be called by a non-admin")
			return nil, nil
		},
	}
	app := newUserTestApp(svc, domain.Claims{UserID: 1, IsAdmin: false})

	req := httptest.NewRequest(fiber.MethodGet, "/users/", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
}

func TestUserHandler_Update_NotFound(t *testing.T) {
	svc := fakeUserService{
		updateFn: func(context.Context, int64, string, string, string) (domain.User, error) {
			return domain.User{}, apperr.ErrNotFound
		},
	}
	app := newUserTestApp(svc, domain.Claims{UserID: 1})

	body, _ := json.Marshal(dto.UpdateUserRequest{Email: "new@example.com"})
	req := httptest.NewRequest(fiber.MethodPatch, "/users/1", bytes.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusNotFound)
	}
}
