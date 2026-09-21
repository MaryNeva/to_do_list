package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
	httptransport "to-do-list/internal/transport/http"
	"to-do-list/internal/transport/http/dto"
	"to-do-list/internal/transport/http/middleware"
)

type fakeAuthValidator struct {
	claims domain.Claims
	err    error
}

func (f fakeAuthValidator) ValidateToken(context.Context, string) (domain.Claims, error) {
	return f.claims, f.err
}

type fakeTaskService struct {
	createFn func(ctx context.Context, actor domain.Claims, title, description string) (domain.Task, error)
	getFn    func(ctx context.Context, actor domain.Claims, id int64) (domain.Task, error)
	listFn   func(ctx context.Context, actor domain.Claims, filter domain.TaskFilter) (domain.Page[domain.Task], error)
	updateFn func(ctx context.Context, actor domain.Claims, id int64, update domain.TaskUpdate) (domain.Task, error)
	deleteFn func(ctx context.Context, actor domain.Claims, id int64) error
	toggleFn func(ctx context.Context, actor domain.Claims, id int64) (domain.Task, error)
}

func (f fakeTaskService) Create(ctx context.Context, actor domain.Claims, title, description string) (domain.Task, error) {
	return f.createFn(ctx, actor, title, description)
}
func (f fakeTaskService) Get(ctx context.Context, actor domain.Claims, id int64) (domain.Task, error) {
	return f.getFn(ctx, actor, id)
}
func (f fakeTaskService) List(ctx context.Context, actor domain.Claims, filter domain.TaskFilter) (domain.Page[domain.Task], error) {
	return f.listFn(ctx, actor, filter)
}
func (f fakeTaskService) Update(ctx context.Context, actor domain.Claims, id int64, update domain.TaskUpdate) (domain.Task, error) {
	return f.updateFn(ctx, actor, id, update)
}
func (f fakeTaskService) Delete(ctx context.Context, actor domain.Claims, id int64) error {
	return f.deleteFn(ctx, actor, id)
}
func (f fakeTaskService) ToggleStatus(ctx context.Context, actor domain.Claims, id int64) (domain.Task, error) {
	return f.toggleFn(ctx, actor, id)
}

func newTaskTestApp(svc TaskService, claims domain.Claims) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: httptransport.NewErrorHandler(silentTestLogger())})
	protected := app.Group("", middleware.Auth(fakeAuthValidator{claims: claims}))
	NewTaskHandler(svc).Mount(protected.Group("/tasks"))
	return app
}

func TestTaskHandler_Create_Success(t *testing.T) {
	svc := fakeTaskService{
		createFn: func(_ context.Context, actor domain.Claims, title, description string) (domain.Task, error) {
			return domain.Task{ID: 1, Title: title, Description: description, CreatorID: actor.UserID, Status: domain.StatusCreated}, nil
		},
	}
	app := newTaskTestApp(svc, domain.Claims{UserID: 5})

	body, _ := json.Marshal(dto.CreateTaskRequest{Title: "Buy milk"})
	req := httptest.NewRequest(fiber.MethodPost, "/tasks/", bytes.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusCreated)
	}

	var got dto.TaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.CreatorID != 5 {
		t.Errorf("CreatorID = %d, want 5", got.CreatorID)
	}
}

func TestTaskHandler_Create_ValidationError(t *testing.T) {
	svc := fakeTaskService{
		createFn: func(context.Context, domain.Claims, string, string) (domain.Task, error) {
			t.Fatal("service should not be called when validation fails")
			return domain.Task{}, nil
		},
	}
	app := newTaskTestApp(svc, domain.Claims{UserID: 5})

	body, _ := json.Marshal(dto.CreateTaskRequest{Title: ""})
	req := httptest.NewRequest(fiber.MethodPost, "/tasks/", bytes.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}

func TestTaskHandler_Get_NotFoundMapsTo404(t *testing.T) {
	svc := fakeTaskService{
		getFn: func(context.Context, domain.Claims, int64) (domain.Task, error) {
			return domain.Task{}, apperr.ErrNotFound
		},
	}
	app := newTaskTestApp(svc, domain.Claims{UserID: 5})

	req := httptest.NewRequest(fiber.MethodGet, "/tasks/123", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusNotFound)
	}
}

func TestTaskHandler_Get_InvalidIDRejected(t *testing.T) {
	svc := fakeTaskService{
		getFn: func(context.Context, domain.Claims, int64) (domain.Task, error) {
			t.Fatal("service should not be called for a non-numeric id")
			return domain.Task{}, nil
		},
	}
	app := newTaskTestApp(svc, domain.Claims{UserID: 5})

	req := httptest.NewRequest(fiber.MethodGet, "/tasks/not-a-number", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}

func TestTaskHandler_RequiresAuthentication(t *testing.T) {
	svc := fakeTaskService{
		listFn: func(context.Context, domain.Claims, domain.TaskFilter) (domain.Page[domain.Task], error) {
			t.Fatal("service should not be called without authentication")
			return domain.Page[domain.Task]{}, nil
		},
	}
	app := fiber.New(fiber.Config{ErrorHandler: httptransport.NewErrorHandler(silentTestLogger())})
	protected := app.Group("", middleware.Auth(fakeAuthValidator{err: apperr.ErrUnauthorized}))
	NewTaskHandler(svc).Mount(protected.Group("/tasks"))

	req := httptest.NewRequest(fiber.MethodGet, "/tasks/", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer invalid")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}
}

func TestTaskHandler_Delete_NoContent(t *testing.T) {
	svc := fakeTaskService{
		deleteFn: func(context.Context, domain.Claims, int64) error { return nil },
	}
	app := newTaskTestApp(svc, domain.Claims{UserID: 5})

	req := httptest.NewRequest(fiber.MethodDelete, "/tasks/1", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}
}

func TestTaskHandler_Update_SeparatesAbsentFromEmpty(t *testing.T) {
	for _, tc := range []struct {
		name            string
		body            string
		wantTitle       *string
		wantDescription *string
		wantStatus      *domain.TaskStatus
	}{
		{
			name:      "only the title is sent",
			body:      `{"title":"Новое название"}`,
			wantTitle: strPtr("Новое название"),
		},
		{
			name:            "an empty description is a request to clear it",
			body:            `{"description":""}`,
			wantDescription: strPtr(""),
		},
		{
			name:      "null reads as absent",
			body:      `{"title":"kept","description":null}`,
			wantTitle: strPtr("kept"),
		},
		{
			name:       "a status on its own",
			body:       `{"status":"completed"}`,
			wantStatus: statusPtr(domain.StatusCompleted),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got domain.TaskUpdate
			svc := fakeTaskService{
				updateFn: func(_ context.Context, _ domain.Claims, _ int64, update domain.TaskUpdate) (domain.Task, error) {
					got = update
					return domain.Task{ID: 1}, nil
				},
			}
			app := newTaskTestApp(svc, domain.Claims{UserID: 5})

			req := httptest.NewRequest(fiber.MethodPatch, "/tasks/1", bytes.NewReader([]byte(tc.body)))
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			req.Header.Set(fiber.HeaderAuthorization, "Bearer token")

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() unexpected error: %v", err)
			}
			if resp.StatusCode != fiber.StatusOK {
				t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
			}

			assertStrPtr(t, "Title", got.Title, tc.wantTitle)
			assertStrPtr(t, "Description", got.Description, tc.wantDescription)

			switch {
			case tc.wantStatus == nil && got.Status != nil:
				t.Errorf("Status = %q, want it to be absent", *got.Status)
			case tc.wantStatus != nil && got.Status == nil:
				t.Errorf("Status is absent, want %q", *tc.wantStatus)
			case tc.wantStatus != nil && *got.Status != *tc.wantStatus:
				t.Errorf("Status = %q, want %q", *got.Status, *tc.wantStatus)
			}
		})
	}
}

func assertStrPtr(t *testing.T, name string, got, want *string) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %q, want it to be absent", name, *got)
	case want != nil && got == nil:
		t.Errorf("%s is absent, want %q", name, *want)
	case want != nil && *got != *want:
		t.Errorf("%s = %q, want %q", name, *got, *want)
	}
}

func strPtr(s string) *string { return &s }

func statusPtr(s domain.TaskStatus) *domain.TaskStatus { return &s }
