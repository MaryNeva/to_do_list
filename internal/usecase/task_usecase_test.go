package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"to-do-list/internal/apperr"
	"to-do-list/internal/auth/password"
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

func (f *fakeTaskRepo) ListByCreator(_ context.Context, creatorID int64, filter domain.TaskFilter) (domain.Page[domain.Task], error) {
	if f.forceErr != nil {
		return domain.Page[domain.Task]{}, f.forceErr
	}

	var matched []domain.Task
	for _, task := range f.tasks {
		if task.CreatorID != creatorID {
			continue
		}
		if filter.Status != nil && task.Status != *filter.Status {
			continue
		}
		matched = append(matched, task)
	}

	sort.Slice(matched, func(i, j int) bool {
		less := matched[i].ID < matched[j].ID
		if filter.Order == domain.OrderDesc {
			return !less
		}
		return less
	})

	total := len(matched)
	if filter.Page.Offset >= total {
		return domain.NewPage([]domain.Task{}, total, filter.Page), nil
	}
	end := filter.Page.Offset + filter.Page.Limit
	if end > total {
		end = total
	}
	return domain.NewPage(matched[filter.Page.Offset:end], total, filter.Page), nil
}

func (f *fakeTaskRepo) Update(_ context.Context, id, ownerID int64, update domain.TaskUpdate) (domain.Task, error) {
	if f.forceErr != nil {
		return domain.Task{}, f.forceErr
	}
	existing, ok := f.tasks[id]
	if !ok || existing.CreatorID != ownerID {
		return domain.Task{}, apperr.ErrNotFound
	}
	if update.Title != nil {
		existing.Title = *update.Title
	}
	if update.Description != nil {
		existing.Description = *update.Description
	}
	if update.Status != nil {
		existing.Status = *update.Status
	}
	existing.UpdatedAt = time.Now()
	f.tasks[id] = existing
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

func (f *fakeTaskRepo) CompareAndSetStatus(_ context.Context, id, ownerID int64, from, to domain.TaskStatus) (domain.Task, error) {
	if f.forceErr != nil {
		return domain.Task{}, f.forceErr
	}
	task, ok := f.tasks[id]
	if !ok || task.CreatorID != ownerID {
		return domain.Task{}, apperr.ErrNotFound
	}
	if task.Status != from {
		return domain.Task{}, apperr.ErrConflict
	}
	task.Status = to
	task.UpdatedAt = time.Now()
	f.tasks[id] = task
	return task, nil
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// actor builds the claims a signed-in user of that id would carry.
func actor(id int64) domain.Claims { return domain.Claims{UserID: id} }

func ptr[T any](v T) *T { return &v }

func testTaskConfig() TaskConfig {
	return TaskConfig{
		Timeout:              time.Second,
		MaxTitleLength:       200,
		MaxDescriptionLength: 4000,
		DefaultPageSize:      20,
		MaxPageSize:          100,
	}
}

func newTaskUseCaseForTest() (*TaskUseCase, *fakeTaskRepo) {
	repo := newFakeTaskRepo()
	return NewTaskUseCase(repo, testTaskConfig(), silentLogger()), repo
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
		{name: "title too long rejected", title: strings.Repeat("a", testTaskConfig().MaxTitleLength+1), wantErr: apperr.ErrValidation},
		{name: "description too long rejected", title: "ok", description: strings.Repeat("a", testTaskConfig().MaxDescriptionLength+1), wantErr: apperr.ErrValidation},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, _ := newTaskUseCaseForTest()

			task, err := uc.Create(context.Background(), actor(1), tt.title, tt.description)

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
		got, err := uc.Get(context.Background(), actor(1), owned.ID)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if got.ID != owned.ID {
			t.Errorf("Get() ID = %d, want %d", got.ID, owned.ID)
		}
	})

	t.Run("non-owner is told not found, not forbidden", func(t *testing.T) {
		_, err := uc.Get(context.Background(), actor(2), owned.ID)
		if !errors.Is(err, apperr.ErrNotFound) {
			t.Errorf("Get() by a different user error = %v, want apperr.ErrNotFound", err)
		}
	})

	t.Run("missing task is not found", func(t *testing.T) {
		_, err := uc.Get(context.Background(), actor(1), 99999)
		if !errors.Is(err, apperr.ErrNotFound) {
			t.Errorf("Get() for a missing task error = %v, want apperr.ErrNotFound", err)
		}
	})
}

func TestTaskUseCase_Update_OwnershipEnforced(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	owned, _ := repo.Create(context.Background(), domain.Task{Title: "mine", CreatorID: 1})

	hijack := domain.TaskUpdate{Title: ptr("hijacked")}
	if _, err := uc.Update(context.Background(), actor(2), owned.ID, hijack); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("Update() by a different user error = %v, want apperr.ErrNotFound", err)
	}

	updated, err := uc.Update(context.Background(), actor(1), owned.ID, domain.TaskUpdate{
		Title:       ptr("renamed"),
		Description: ptr("new description"),
	})
	if err != nil {
		t.Fatalf("Update() by the owner unexpected error: %v", err)
	}
	if updated.Title != "renamed" {
		t.Errorf("Update() Title = %q, want %q", updated.Title, "renamed")
	}
}

func TestTaskUseCase_Update_PartialSemantics(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("x", 201)

	for _, tc := range []struct {
		name            string
		update          domain.TaskUpdate
		wantErr         error
		wantTitle       string
		wantDescription string
		wantStatus      domain.TaskStatus
	}{
		{
			name:   "absent fields keep their values",
			update: domain.TaskUpdate{Title: ptr("renamed")},
			// This is the bug this test exists for: renaming used to blank
			// the description, because the transport sent one either way.
			wantTitle: "renamed", wantDescription: "the original description", wantStatus: domain.StatusInProgress,
		},
		{
			name:      "an empty description clears it",
			update:    domain.TaskUpdate{Description: ptr("")},
			wantTitle: "the original title", wantDescription: "", wantStatus: domain.StatusInProgress,
		},
		{
			name:      "whitespace is trimmed before it is stored",
			update:    domain.TaskUpdate{Title: ptr("  padded  "), Description: ptr("  also padded  ")},
			wantTitle: "padded", wantDescription: "also padded", wantStatus: domain.StatusInProgress,
		},
		{
			name:      "status is set to exactly what was asked for",
			update:    domain.TaskUpdate{Status: ptr(domain.StatusCompleted)},
			wantTitle: "the original title", wantDescription: "the original description", wantStatus: domain.StatusCompleted,
		},
		{
			name:   "an empty title is rejected rather than stored",
			update: domain.TaskUpdate{Title: ptr("   ")}, wantErr: apperr.ErrValidation,
		},
		{
			name:   "an unknown status is rejected",
			update: domain.TaskUpdate{Status: ptr(domain.TaskStatus("done"))}, wantErr: apperr.ErrValidation,
		},
		{
			name:   "an over-long title is rejected",
			update: domain.TaskUpdate{Title: ptr(long)}, wantErr: apperr.ErrValidation,
		},
		{
			name:   "an empty body is rejected",
			update: domain.TaskUpdate{}, wantErr: apperr.ErrValidation,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc, repo := newTaskUseCaseForTest()
			task, _ := repo.Create(ctx, domain.Task{
				Title:       "the original title",
				Description: "the original description",
				Status:      domain.StatusInProgress,
				CreatorID:   1,
			})

			updated, err := uc.Update(ctx, actor(1), task.ID, tc.update)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Update() error = %v, want %v", err, tc.wantErr)
				}
				stored := repo.tasks[task.ID]
				if stored.Title != task.Title || stored.Description != task.Description || stored.Status != task.Status {
					t.Errorf("a rejected update still changed the task: %+v", stored)
				}
				return
			}

			if err != nil {
				t.Fatalf("Update() unexpected error: %v", err)
			}
			if updated.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", updated.Title, tc.wantTitle)
			}
			if updated.Description != tc.wantDescription {
				t.Errorf("Description = %q, want %q", updated.Description, tc.wantDescription)
			}
			if updated.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", updated.Status, tc.wantStatus)
			}
		})
	}
}

// What toggle-status cannot promise: the same call twice leaves the task
// where the caller asked for it, so a retry after a lost response is safe.
func TestTaskUseCase_Update_SettingAStatusIsIdempotent(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	ctx := context.Background()
	task, _ := repo.Create(ctx, domain.Task{Title: "t", Status: domain.StatusCreated, CreatorID: 1})

	completed := domain.TaskUpdate{Status: ptr(domain.StatusCompleted)}
	for attempt := 1; attempt <= 3; attempt++ {
		updated, err := uc.Update(ctx, actor(1), task.ID, completed)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if updated.Status != domain.StatusCompleted {
			t.Fatalf("attempt %d: Status = %q, want %q", attempt, updated.Status, domain.StatusCompleted)
		}
	}

	// The same three calls through ToggleStatus would end on in_progress.
	if got := repo.tasks[task.ID].Status; got != domain.StatusCompleted {
		t.Errorf("stored Status = %q, want %q", got, domain.StatusCompleted)
	}
}

func TestTaskUseCase_Delete_OwnershipEnforced(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	owned, _ := repo.Create(context.Background(), domain.Task{Title: "mine", CreatorID: 1})

	if err := uc.Delete(context.Background(), actor(2), owned.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("Delete() by a different user error = %v, want apperr.ErrNotFound", err)
	}
	if _, ok := repo.tasks[owned.ID]; !ok {
		t.Error("Delete() by a non-owner must not remove the task")
	}

	if err := uc.Delete(context.Background(), actor(1), owned.ID); err != nil {
		t.Fatalf("Delete() by the owner unexpected error: %v", err)
	}
	if _, ok := repo.tasks[owned.ID]; ok {
		t.Error("Delete() by the owner should remove the task")
	}
}

func TestTaskUseCase_ToggleStatus_Cycles(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	owned, _ := repo.Create(context.Background(), domain.Task{Title: "mine", CreatorID: 1, Status: domain.StatusCreated})

	wantSequence := []domain.TaskStatus{
		domain.StatusInProgress,
		domain.StatusCompleted,
		domain.StatusCreated,
	}

	for i, want := range wantSequence {
		got, err := uc.ToggleStatus(context.Background(), actor(1), owned.ID)
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

	if _, err := uc.ToggleStatus(context.Background(), actor(2), owned.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("ToggleStatus() by a different user error = %v, want apperr.ErrNotFound", err)
	}
}

func TestTaskUseCase_List_ScopedToRequester(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	repo.Create(context.Background(), domain.Task{Title: "user1-a", CreatorID: 1})
	repo.Create(context.Background(), domain.Task{Title: "user1-b", CreatorID: 1})
	repo.Create(context.Background(), domain.Task{Title: "user2-a", CreatorID: 2})

	page, err := uc.List(context.Background(), actor(1), domain.TaskFilter{})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("List() returned %d tasks, want 2", len(page.Items))
	}
	for _, task := range page.Items {
		if task.CreatorID != 1 {
			t.Errorf("List() returned a task owned by %d, want only 1", task.CreatorID)
		}
	}
}

func TestTaskUseCase_Create_CountsCharactersNotBytes(t *testing.T) {
	cfg := testTaskConfig()

	tests := []struct {
		name        string
		title       string
		description string
		wantErr     bool
	}{
		{"cyrillic title at the limit", strings.Repeat("я", cfg.MaxTitleLength), "", false},
		{"cyrillic title one over", strings.Repeat("я", cfg.MaxTitleLength+1), "", true},
		{"emoji title at the limit", strings.Repeat("🚀", cfg.MaxTitleLength), "", false},
		{"emoji title one over", strings.Repeat("🚀", cfg.MaxTitleLength+1), "", true},
		{"cyrillic description at the limit", "ok", strings.Repeat("я", cfg.MaxDescriptionLength), false},
		{"cyrillic description one over", "ok", strings.Repeat("я", cfg.MaxDescriptionLength+1), true},
		{"emoji description at the limit", "ok", strings.Repeat("🚀", cfg.MaxDescriptionLength), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, _ := newTaskUseCaseForTest()

			task, err := uc.Create(context.Background(), actor(1), tt.title, tt.description)
			if tt.wantErr {
				if !errors.Is(err, apperr.ErrValidation) {
					t.Errorf("Create() with %d characters error = %v, want apperr.ErrValidation",
						utf8.RuneCountInString(tt.title), err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() with %d characters (%d bytes) unexpected error: %v",
					utf8.RuneCountInString(tt.title), len(tt.title), err)
			}
			if utf8.RuneCountInString(task.Title) != utf8.RuneCountInString(tt.title) {
				t.Errorf("stored title has %d characters, want %d",
					utf8.RuneCountInString(task.Title), utf8.RuneCountInString(tt.title))
			}
		})
	}
}

func TestTaskUseCase_ToggleStatus_RetriesAfterConcurrentChange(t *testing.T) {
	repo := newFakeTaskRepo()
	interfering := &interferingTaskRepo{fakeTaskRepo: repo}
	uc := NewTaskUseCase(interfering, testTaskConfig(), silentLogger())

	owned, _ := repo.Create(context.Background(), domain.Task{Title: "mine", CreatorID: 1, Status: domain.StatusCreated})

	interfering.before = func() {
		task := repo.tasks[owned.ID]
		task.Status = domain.StatusInProgress
		repo.tasks[owned.ID] = task
	}

	got, err := uc.ToggleStatus(context.Background(), actor(1), owned.ID)
	if err != nil {
		t.Fatalf("ToggleStatus() unexpected error: %v", err)
	}
	if got.Status != domain.StatusCompleted {
		t.Errorf("Status = %q, want %q", got.Status, domain.StatusCompleted)
	}
}

// interferingTaskRepo runs a hook once, just before the first
// compare-and-set, to simulate another request winning the race.
type interferingTaskRepo struct {
	*fakeTaskRepo
	before func()
}

func (r *interferingTaskRepo) CompareAndSetStatus(ctx context.Context, id, ownerID int64, from, to domain.TaskStatus) (domain.Task, error) {
	if r.before != nil {
		hook := r.before
		r.before = nil
		hook()
	}
	return r.fakeTaskRepo.CompareAndSetStatus(ctx, id, ownerID, from, to)
}

// TestTaskUseCase_ToggleStatus_ForeignTaskStaysNotFound: the ownership check
// must survive the move to a conditional update.
func TestTaskUseCase_ToggleStatus_ForeignTaskStaysNotFound(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	owned, _ := repo.Create(context.Background(), domain.Task{Title: "mine", CreatorID: 1, Status: domain.StatusCreated})

	if _, err := uc.ToggleStatus(context.Background(), actor(2), owned.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("ToggleStatus() by a different user error = %v, want apperr.ErrNotFound", err)
	}
	if repo.tasks[owned.ID].Status != domain.StatusCreated {
		t.Errorf("a foreign toggle changed the status to %q", repo.tasks[owned.ID].Status)
	}
}

func TestTaskUseCase_List_AppliesPaginationDefaultsAndLimits(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	cfg := testTaskConfig()
	for i := 0; i < 25; i++ {
		repo.Create(context.Background(), domain.Task{Title: "t", CreatorID: 1, Status: domain.StatusCreated})
	}

	tests := []struct {
		name      string
		request   domain.PageRequest
		wantItems int
		wantLimit int
	}{
		{"no limit falls back to the default", domain.PageRequest{}, cfg.DefaultPageSize, cfg.DefaultPageSize},
		{"explicit limit is honoured", domain.PageRequest{Limit: 5}, 5, 5},
		{"limit above the maximum is trimmed", domain.PageRequest{Limit: 1000}, 25, cfg.MaxPageSize},
		{"offset past the end yields no items", domain.PageRequest{Limit: 10, Offset: 100}, 0, 10},
		{"negative offset starts at the beginning", domain.PageRequest{Limit: 3, Offset: -5}, 3, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := uc.List(context.Background(), actor(1), domain.TaskFilter{Page: tt.request})
			if err != nil {
				t.Fatalf("List() unexpected error: %v", err)
			}
			if len(page.Items) != tt.wantItems {
				t.Errorf("items = %d, want %d", len(page.Items), tt.wantItems)
			}
			if page.Limit != tt.wantLimit {
				t.Errorf("Limit = %d, want %d", page.Limit, tt.wantLimit)
			}
			if page.Total != 25 {
				t.Errorf("Total = %d, want 25 regardless of the page size", page.Total)
			}
		})
	}
}

func TestTaskUseCase_List_FiltersByStatus(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	repo.Create(context.Background(), domain.Task{Title: "a", CreatorID: 1, Status: domain.StatusCreated})
	repo.Create(context.Background(), domain.Task{Title: "b", CreatorID: 1, Status: domain.StatusInProgress})
	repo.Create(context.Background(), domain.Task{Title: "c", CreatorID: 1, Status: domain.StatusInProgress})

	inProgress := domain.StatusInProgress
	page, err := uc.List(context.Background(), actor(1), domain.TaskFilter{Status: &inProgress})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("got %d of %d tasks, want 2 of 2", len(page.Items), page.Total)
	}
	for _, task := range page.Items {
		if task.Status != domain.StatusInProgress {
			t.Errorf("filtered list contains a task with status %q", task.Status)
		}
	}
}

func TestTaskUseCase_List_RejectsUnknownStatus(t *testing.T) {
	uc, _ := newTaskUseCaseForTest()

	unknown := domain.TaskStatus("archived")
	_, err := uc.List(context.Background(), actor(1), domain.TaskFilter{Status: &unknown})
	if !errors.Is(err, apperr.ErrValidation) {
		t.Errorf("List() with an unknown status error = %v, want apperr.ErrValidation", err)
	}
}

func TestTaskUseCase_List_StaysScopedToRequesterWhenPaginated(t *testing.T) {
	uc, repo := newTaskUseCaseForTest()
	for i := 0; i < 5; i++ {
		repo.Create(context.Background(), domain.Task{Title: "mine", CreatorID: 1, Status: domain.StatusCreated})
		repo.Create(context.Background(), domain.Task{Title: "theirs", CreatorID: 2, Status: domain.StatusCreated})
	}

	page, err := uc.List(context.Background(), actor(1), domain.TaskFilter{Page: domain.PageRequest{Limit: 100}})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if page.Total != 5 {
		t.Errorf("Total = %d, want 5: the count must be scoped to the requester too", page.Total)
	}
	for _, task := range page.Items {
		if task.CreatorID != 1 {
			t.Errorf("page contains a task owned by %d", task.CreatorID)
		}
	}
}

func hasherMatches(t *testing.T, hash, plain string) bool {
	t.Helper()

	h, err := password.NewHasher(bcrypt.MinCost, 2)
	if err != nil {
		t.Fatalf("password.NewHasher(): %v", err)
	}

	err = h.Verify(context.Background(), hash, plain)
	if err != nil && !errors.Is(err, password.ErrMismatch) {
		t.Fatalf("Verify(): %v", err)
	}
	return err == nil
}
