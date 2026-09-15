package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

type fakeTaskRepo struct {
	tasks    map[int64]domain.Task
	nextID   int64
	forceErr error
}

func newFakeTaskRepo() *fakeTaskRepo {
	return &fakeTaskRepo{tasks: make(map[int64]domain.Task), nextID: 1}
}

func (f *fakeTaskRepo) Create(_ context.Context, task domain.Task) (domain.Task, error) {
	if f.forceErr != nil {
		return domain.Task{}, f.forceErr
	}
	task.ID = f.nextID
	f.nextID++
	now := time.Now()
	task.CreatedAt, task.UpdatedAt = now, now
	f.tasks[task.ID] = task
	return task, nil
}

func (f *fakeTaskRepo) GetByID(_ context.Context, id int64) (domain.Task, error) {
	if f.forceErr != nil {
		return domain.Task{}, f.forceErr
	}
	task, ok := f.tasks[id]
	if !ok {
		return domain.Task{}, apperr.ErrNotFound
	}
	return task, nil
}

func (f *fakeTaskRepo) ListByCreator(_ context.Context, creatorID int64) ([]domain.Task, error) {
	if f.forceErr != nil {
		return nil, f.forceErr
	}
	var result []domain.Task
	for _, task := range f.tasks {
		if task.CreatorID == creatorID {
			result = append(result, task)
		}
	}
	return result, nil
}

func (f *fakeTaskRepo) Update(_ context.Context, task domain.Task) (domain.Task, error) {
	if f.forceErr != nil {
		return domain.Task{}, f.forceErr
	}
	existing, ok := f.tasks[task.ID]
	if !ok {
		return domain.Task{}, apperr.ErrNotFound
	}
	existing.Title = task.Title
	existing.Description = task.Description
	existing.UpdatedAt = time.Now()
	f.tasks[task.ID] = existing
	return existing, nil
}

func (f *fakeTaskRepo) Delete(_ context.Context, id int64) error {
	if f.forceErr != nil {
		return f.forceErr
	}
	if _, ok := f.tasks[id]; !ok {
		return apperr.ErrNotFound
	}
	delete(f.tasks, id)
	return nil
}

func (f *fakeTaskRepo) UpdateStatus(_ context.Context, id int64, status domain.TaskStatus) error {
	if f.forceErr != nil {
		return f.forceErr
	}
	task, ok := f.tasks[id]
	if !ok {
		return apperr.ErrNotFound
	}
	task.Status = status
	task.UpdatedAt = time.Now()
	f.tasks[id] = task
	return nil
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTaskUseCaseForTest() (*TaskUseCase, *fakeTaskRepo) {
	repo := newFakeTaskRepo()
	return NewTaskUseCase(repo, time.Second, silentLogger()), repo
}

func TestTaskUseCase_Create(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
		wantErr     error
	}{
		{name: "valid task", title: "Buy milk", description: "2%"},
		{name: "trims whitespace", title: "  Buy milk  ", description: "  2%  "},
		{name: "empty title rejected", title: "   ", wantErr: apperr.ErrValidation},
		{name: "title too long rejected", title: strings.Repeat("a", maxTaskTitleLen+1), wantErr: apperr.ErrValidation},
		{name: "description too long rejected", title: "ok", description: strings.Repeat("a", maxTaskDescLen+1), wantErr: apperr.ErrValidation},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, _ := newTaskUseCaseForTest()

			task, err := uc.Create(context.Background(), 1, tt.title, tt.description)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Create() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() unexpected error: %v", err)
			}
			if task.ID == 0 {
				t.Error("Create() did not assign an ID")
			}
			if task.Status != domain.StatusCreated {
				t.Errorf("Create() Status = %q, want %q", task.Status, domain.StatusCreated)
			}
			if task.Title != strings.TrimSpace(tt.title) {
				t.Errorf("Create() Title = %q, want trimmed %q", task.Title, strings.TrimSpace(tt.title))
			}
			if task.CreatorID != 1 {
				t.Errorf("Create() CreatorID = %d, want 1", task.CreatorID)
			}
		})
	}
}

func TestTaskUseCase_Get_OwnershipEnforced(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	owned, err := repo.Create(context.Background(), domain.Task{Title: "mine", CreatorID: 1})
	if err != nil {
		t.Fatalf("seed Create() unexpected error: %v", err)
	}

	t.Run("owner can read", func(t *testing.T) {
		got, err := uc.Get(context.Background(), 1, owned.ID)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if got.ID != owned.ID {
			t.Errorf("Get() ID = %d, want %d", got.ID, owned.ID)
		}
	})

	t.Run("non-owner is told not found, not forbidden", func(t *testing.T) {
		_, err := uc.Get(context.Background(), 2, owned.ID)
		if !errors.Is(err, apperr.ErrNotFound) {
			t.Errorf("Get() by a different user error = %v, want apperr.ErrNotFound", err)
		}
	})

	t.Run("missing task is not found", func(t *testing.T) {
		_, err := uc.Get(context.Background(), 1, 99999)
		if !errors.Is(err, apperr.ErrNotFound) {
			t.Errorf("Get() for a missing task error = %v, want apperr.ErrNotFound", err)
		}
	})
}

func TestTaskUseCase_Update_OwnershipEnforced(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	owned, _ := repo.Create(context.Background(), domain.Task{Title: "mine", CreatorID: 1})

	if _, err := uc.Update(context.Background(), 2, owned.ID, "hijacked", ""); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("Update() by a different user error = %v, want apperr.ErrNotFound", err)
	}

	updated, err := uc.Update(context.Background(), 1, owned.ID, "renamed", "new description")
	if err != nil {
		t.Fatalf("Update() by the owner unexpected error: %v", err)
	}
	if updated.Title != "renamed" {
		t.Errorf("Update() Title = %q, want %q", updated.Title, "renamed")
	}
}

func TestTaskUseCase_Delete_OwnershipEnforced(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	owned, _ := repo.Create(context.Background(), domain.Task{Title: "mine", CreatorID: 1})

	if err := uc.Delete(context.Background(), 2, owned.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("Delete() by a different user error = %v, want apperr.ErrNotFound", err)
	}
	if _, ok := repo.tasks[owned.ID]; !ok {
		t.Error("Delete() by a non-owner must not remove the task")
	}

	if err := uc.Delete(context.Background(), 1, owned.ID); err != nil {
		t.Fatalf("Delete() by the owner unexpected error: %v", err)
	}
	if _, ok := repo.tasks[owned.ID]; ok {
		t.Error("Delete() by the owner should remove the task")
	}
}

func TestTaskUseCase_ToggleStatus_Cycles(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	owned, _ := repo.Create(context.Background(), domain.Task{Title: "mine", CreatorID: 1})

	wantSequence := []domain.TaskStatus{
		domain.StatusInProgress,
		domain.StatusCompleted,
		domain.StatusCreated,
	}

	for i, want := range wantSequence {
		got, err := uc.ToggleStatus(context.Background(), 1, owned.ID)
		if err != nil {
			t.Fatalf("ToggleStatus() call #%d unexpected error: %v", i+1, err)
		}
		if got.Status != want {
			t.Errorf("ToggleStatus() call #%d Status = %q, want %q", i+1, got.Status, want)
		}
	}
}

func TestTaskUseCase_ToggleStatus_OwnershipEnforced(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	owned, _ := repo.Create(context.Background(), domain.Task{Title: "mine", CreatorID: 1, Status: domain.StatusCreated})

	if _, err := uc.ToggleStatus(context.Background(), 2, owned.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("ToggleStatus() by a different user error = %v, want apperr.ErrNotFound", err)
	}
}

func TestTaskUseCase_List_ScopedToRequester(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	repo.Create(context.Background(), domain.Task{Title: "user1-a", CreatorID: 1})
	repo.Create(context.Background(), domain.Task{Title: "user1-b", CreatorID: 1})
	repo.Create(context.Background(), domain.Task{Title: "user2-a", CreatorID: 2})

	list, err := uc.List(context.Background(), 1)
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("List() returned %d tasks, want 2", len(list))
	}
	for _, task := range list {
		if task.CreatorID != 1 {
			t.Errorf("List() returned a task owned by %d, want only 1", task.CreatorID)
		}
	}
}
