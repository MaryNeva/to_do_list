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
	getFn    func(ctx context.Context, actor domain.Claims, id int64) (domain.User, error)
	listFn   func(ctx context.Context, actor domain.Claims, page domain.PageRequest) (domain.Page[domain.User], error)
	updateFn func(ctx context.Context, actor domain.Claims, id int64, edit domain.UserEdit) (domain.User, error)
	deleteFn func(ctx context.Context, actor domain.Claims, id int64) error
}

func (f fakeUserService) Get(ctx context.Context, actor domain.Claims, id int64) (domain.User, error) {
	return f.getFn(ctx, actor, id)
}
func (f fakeUserService) List(ctx context.Context, actor domain.Claims, page domain.PageRequest) (domain.Page[domain.User], error) {
	return f.listFn(ctx, actor, page)
}
func (f fakeUserService) Update(ctx context.Context, actor domain.Claims, id int64, edit domain.UserEdit) (domain.User, error) {
	return f.updateFn(ctx, actor, id, edit)
}
func (f fakeUserService) Delete(ctx context.Context, actor domain.Claims, id int64) error {
	return f.deleteFn(ctx, actor, id)
}

func newUserTestApp(svc UserService, claims domain.Claims) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: httptransport.NewErrorHandler(silentTestLogger())})
	protected := app.Group("", middleware.Auth(fakeAuthValidator{claims: claims}))
	NewUserHandler(svc).Mount(protected.Group("/users"))
	return app
}

func TestUserHandler_Get_NeverLeaksPasswordHash(t *testing.T) {
	const secretHash = "$2a$12$thisIsASecretBcryptHashThatMustNeverLeak"

	svc := fakeUserService{
		getFn: func(_ context.Context, actor domain.Claims, id int64) (domain.User, error) {
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
		getFn: func(_ context.Context, actor domain.Claims, id int64) (domain.User, error) {
			if actor.UserID != 1 || actor.IsAdmin || id != 2 {
				t.Fatal("actor or target not forwarded")
			}
			return domain.User{}, apperr.ErrForbidden
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
		getFn: func(_ context.Context, actor domain.Claims, id int64) (domain.User, error) {
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
		getFn: func(_ context.Context, actor domain.Claims, id int64) (domain.User, error) {
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
		listFn: func(_ context.Context, actor domain.Claims, _ domain.PageRequest) (domain.Page[domain.User], error) {
			if actor.UserID != 1 || actor.IsAdmin {
				t.Fatal("actor not forwarded")
			}
			return domain.Page[domain.User]{}, apperr.ErrForbidden
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
		updateFn: func(context.Context, domain.Claims, int64, domain.UserEdit) (domain.User, error) {
			return domain.User{}, apperr.ErrNotFound
		},
	}
	app := newUserTestApp(svc, domain.Claims{UserID: 1})

	body, _ := json.Marshal(dto.UpdateUserRequest{Email: strPtr("new@example.com")})
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

func TestUserHandler_Update_TellsAnAbsentFieldFromAnEmptyOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want domain.UserEdit
	}{
		{
			name: "an empty object edits nothing",
			body: `{}`,
			want: domain.UserEdit{},
		},
		{
			name: "null reads as absent",
			body: `{"username":null,"email":null,"password":null}`,
			want: domain.UserEdit{},
		},
		{
			name: "one field sent, the others left alone",
			body: `{"username":"bob"}`,
			want: domain.UserEdit{Username: strPtr("bob")},
		},
		{
			name: "an empty string is sent on as an empty string, not as silence",
			body: `{"username":""}`,
			want: domain.UserEdit{Username: strPtr("")},
		},
		{
			name: "every field at once",
			body: `{"username":"bob","email":"bob@example.com","password":"a-new-password"}`,
			want: domain.UserEdit{
				Username: strPtr("bob"),
				Email:    strPtr("bob@example.com"),
				Password: strPtr("a-new-password"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got domain.UserEdit
			svc := fakeUserService{
				updateFn: func(_ context.Context, _ domain.Claims, _ int64, edit domain.UserEdit) (domain.User, error) {
					got = edit
					return domain.User{ID: 1, Username: "bob", Email: "bob@example.com"}, nil
				},
			}
			app := newUserTestApp(svc, domain.Claims{UserID: 1})

			req := httptest.NewRequest(fiber.MethodPatch, "/users/1", strings.NewReader(tc.body))
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() unexpected error: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != fiber.StatusOK {
				t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
			}

			assertSameEdit(t, got, tc.want)
		})
	}
}

func TestUserHandler_Update_ReportsARefusedEditAsABadRequest(t *testing.T) {
	svc := fakeUserService{
		updateFn: func(context.Context, domain.Claims, int64, domain.UserEdit) (domain.User, error) {
			return domain.User{}, apperr.ErrValidation
		},
	}
	app := newUserTestApp(svc, domain.Claims{UserID: 1})

	req := httptest.NewRequest(fiber.MethodPatch, "/users/1", strings.NewReader(`{}`))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}

func assertSameEdit(t *testing.T, got, want domain.UserEdit) {
	t.Helper()

	for _, field := range []struct {
		name      string
		got, want *string
	}{
		{"username", got.Username, want.Username},
		{"email", got.Email, want.Email},
		{"password", got.Password, want.Password},
	} {
		switch {
		case field.got == nil && field.want == nil:
		case field.got == nil:
			t.Errorf("%s reached the service as absent, want %q", field.name, *field.want)
		case field.want == nil:
			t.Errorf("%s reached the service as %q, want absent", field.name, *field.got)
		case *field.got != *field.want:
			t.Errorf("%s reached the service as %q, want %q", field.name, *field.got, *field.want)
		}
	}
}

func TestUserHandler_Update_JudgesTheEmailOnlyWhenItWasSent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"not an address", `{"email":"nope"}`, fiber.StatusBadRequest},
		{"present but empty", `{"email":""}`, fiber.StatusBadRequest},
		{"absent", `{"username":"bob"}`, fiber.StatusOK},
		{"null", `{"email":null,"username":"bob"}`, fiber.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := fakeUserService{
				updateFn: func(context.Context, domain.Claims, int64, domain.UserEdit) (domain.User, error) {
					return domain.User{ID: 1, Username: "bob"}, nil
				},
			}
			app := newUserTestApp(svc, domain.Claims{UserID: 1})

			req := httptest.NewRequest(fiber.MethodPatch, "/users/1", strings.NewReader(tc.body))
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() unexpected error: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}
