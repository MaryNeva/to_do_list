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
	createFn func(ctx context.Context, creatorID int64, title, description string) (domain.Task, error)
	getFn    func(ctx context.Context, requesterID, id int64) (domain.Task, error)
	listFn   func(ctx context.Context, requesterID int64, filter domain.TaskFilter) (domain.Page[domain.Task], error)
	updateFn func(ctx context.Context, requesterID, id int64, title, description string) (domain.Task, error)
	deleteFn func(ctx context.Context, requesterID, id int64) error
	toggleFn func(ctx context.Context, requesterID, id int64) (domain.Task, error)
}

func (f fakeTaskService) Create(ctx context.Context, creatorID int64, title, description string) (domain.Task, error) {
	return f.createFn(ctx, creatorID, title, description)
}
func (f fakeTaskService) Get(ctx context.Context, requesterID, id int64) (domain.Task, error) {
	return f.getFn(ctx, requesterID, id)
}
func (f fakeTaskService) List(ctx context.Context, requesterID int64, filter domain.TaskFilter) (domain.Page[domain.Task], error) {
	return f.listFn(ctx, requesterID, filter)
}
func (f fakeTaskService) Update(ctx context.Context, requesterID, id int64, title, description string) (domain.Task, error) {
	return f.updateFn(ctx, requesterID, id, title, description)
}
func (f fakeTaskService) Delete(ctx context.Context, requesterID, id int64) error {
	return f.deleteFn(ctx, requesterID, id)
}
func (f fakeTaskService) ToggleStatus(ctx context.Context, requesterID, id int64) (domain.Task, error) {
	return f.toggleFn(ctx, requesterID, id)
}

func newTaskTestApp(svc domain.TaskService, claims domain.Claims) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: httptransport.NewErrorHandler(silentTestLogger())})
	protected := app.Group("", middleware.Auth(fakeAuthValidator{claims: claims}))
	NewTaskHandler(svc).Mount(protected.Group("/tasks"))
	return app
}

func TestTaskHandler_Create_Success(t *testing.T) {
	svc := fakeTaskService{
		createFn: func(_ context.Context, creatorID int64, title, description string) (domain.Task, error) {
			return domain.Task{ID: 1, Title: title, Description: description, CreatorID: creatorID, Status: domain.StatusCreated}, nil
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
		createFn: func(context.Context, int64, string, string) (domain.Task, error) {
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
		getFn: func(context.Context, int64, int64) (domain.Task, error) {
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
		getFn: func(context.Context, int64, int64) (domain.Task, error) {
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
		listFn: func(context.Context, int64, domain.TaskFilter) (domain.Page[domain.Task], error) {
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
		deleteFn: func(context.Context, int64, int64) error { return nil },
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
