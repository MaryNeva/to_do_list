package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
	httptransport "to-do-list/internal/transport/http"
	"to-do-list/internal/transport/http/dto"
	"to-do-list/internal/transport/http/middleware"
)

type fakeAuthService struct {
	registerFn func(ctx context.Context, username, email, password string) (domain.User, error)
	loginFn    func(ctx context.Context, username, password string) (string, time.Time, domain.User, error)
	validateFn func(ctx context.Context, token string) (domain.Claims, error)
}

func (f fakeAuthService) Register(ctx context.Context, username, email, password string) (domain.User, error) {
	return f.registerFn(ctx, username, email, password)
}
func (f fakeAuthService) Login(ctx context.Context, username, password string) (string, time.Time, domain.User, error) {
	return f.loginFn(ctx, username, password)
}
func (f fakeAuthService) ValidateToken(ctx context.Context, token string) (domain.Claims, error) {
	return f.validateFn(ctx, token)
}

func newAuthTestApp(svc domain.AuthService) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: httptransport.NewErrorHandler(silentTestLogger())})
	h := NewAuthHandler(svc)
	auth := app.Group("/auth")
	auth.Post("/register", h.Register)
	auth.Post("/login", h.Login)
	auth.Get("/me", middleware.Auth(svc), h.Me)
	return app
}

func TestAuthHandler_Register_Success(t *testing.T) {
	svc := fakeAuthService{
		registerFn: func(_ context.Context, username, email, password string) (domain.User, error) {
			return domain.User{ID: 1, Username: username, Email: email}, nil
		},
	}
	app := newAuthTestApp(svc)

	body, _ := json.Marshal(dto.RegisterRequest{Username: "alice", Email: "alice@example.com", Password: "s3cret-pass"})
	req := httptest.NewRequest(fiber.MethodPost, "/auth/register", bytes.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusCreated)
	}
}

func TestAuthHandler_Register_DuplicateConflict(t *testing.T) {
	svc := fakeAuthService{
		registerFn: func(context.Context, string, string, string) (domain.User, error) {
			return domain.User{}, apperr.ErrConflict
		},
	}
	app := newAuthTestApp(svc)

	body, _ := json.Marshal(dto.RegisterRequest{Username: "alice", Email: "alice@example.com", Password: "s3cret-pass"})
	req := httptest.NewRequest(fiber.MethodPost, "/auth/register", bytes.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusConflict)
	}
}

func TestAuthHandler_Register_InvalidEmailRejected(t *testing.T) {
	svc := fakeAuthService{
		registerFn: func(context.Context, string, string, string) (domain.User, error) {
			t.Fatal("service should not be called for an invalid email")
			return domain.User{}, nil
		},
	}
	app := newAuthTestApp(svc)

	body, _ := json.Marshal(dto.RegisterRequest{Username: "alice", Email: "not-an-email", Password: "s3cret-pass"})
	req := httptest.NewRequest(fiber.MethodPost, "/auth/register", bytes.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}

func TestAuthHandler_Login_Success(t *testing.T) {
	svc := fakeAuthService{
		loginFn: func(context.Context, string, string) (string, time.Time, domain.User, error) {
			return "a-jwt-token", time.Now().Add(time.Hour), domain.User{ID: 1, Username: "alice"}, nil
		},
	}
	app := newAuthTestApp(svc)

	body, _ := json.Marshal(dto.LoginRequest{Username: "alice", Password: "s3cret-pass"})
	req := httptest.NewRequest(fiber.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	var got dto.LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Token != "a-jwt-token" {
		t.Errorf("Token = %q, want %q", got.Token, "a-jwt-token")
	}
}

func TestAuthHandler_Login_InvalidCredentials(t *testing.T) {
	svc := fakeAuthService{
		loginFn: func(context.Context, string, string) (string, time.Time, domain.User, error) {
			return "", time.Time{}, domain.User{}, apperr.ErrInvalidCredentials
		},
	}
	app := newAuthTestApp(svc)

	body, _ := json.Marshal(dto.LoginRequest{Username: "alice", Password: "wrong"})
	req := httptest.NewRequest(fiber.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}
}

func TestAuthHandler_Me_ReturnsCallerIdentity(t *testing.T) {
	svc := fakeAuthService{
		validateFn: func(context.Context, string) (domain.Claims, error) {
			return domain.Claims{UserID: 7, Username: "alice", IsAdmin: false}, nil
		},
	}
	app := newAuthTestApp(svc)

	req := httptest.NewRequest(fiber.MethodGet, "/auth/me", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer a-valid-token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	var got dto.MeResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.UserID != 7 || got.Username != "alice" {
		t.Errorf("Me() = %+v, want UserID=7 Username=alice", got)
	}
}
