//go:build integration

package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

func seedTasks(t *testing.T, repo *TaskRepository, creatorID int64, statuses ...domain.TaskStatus) []domain.Task {
	t.Helper()
	var created []domain.Task
	for i, status := range statuses {
		task, err := repo.Create(context.Background(), domain.Task{
			Title:     string(rune('a'+i)) + "-task",
			Status:    status,
			CreatorID: creatorID,
		})
		if err != nil {
			t.Fatalf("seed task: %v", err)
		}
		created = append(created, task)
		time.Sleep(time.Millisecond)
	}
	return created
}

func TestListByCreator_PaginatesAndCountsTotal(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	repo := NewTaskRepository(pool)

	statuses := make([]domain.TaskStatus, 7)
	for i := range statuses {
		statuses[i] = domain.StatusCreated
	}
	seedTasks(t, repo, user.ID, statuses...)

	page, err := repo.ListByCreator(context.Background(), user.ID, domain.TaskFilter{
		Page: domain.PageRequest{Limit: 3, Offset: 0},
	})
	if err != nil {
		t.Fatalf("ListByCreator(): %v", err)
	}
	if len(page.Items) != 3 {
		t.Errorf("first page has %d items, want 3", len(page.Items))
	}
	if page.Total != 7 {
		t.Errorf("Total = %d, want 7", page.Total)
	}

	last, err := repo.ListByCreator(context.Background(), user.ID, domain.TaskFilter{
		Page: domain.PageRequest{Limit: 3, Offset: 6},
	})
	if err != nil {
		t.Fatalf("ListByCreator(): %v", err)
	}
	if len(last.Items) != 1 {
		t.Errorf("last page has %d items, want 1", len(last.Items))
	}

	beyond, err := repo.ListByCreator(context.Background(), user.ID, domain.TaskFilter{
		Page: domain.PageRequest{Limit: 3, Offset: 50},
	})
	if err != nil {
		t.Fatalf("ListByCreator(): %v", err)
	}
	if len(beyond.Items) != 0 {
		t.Errorf("page past the end has %d items, want 0", len(beyond.Items))
	}
	if beyond.Items == nil {
		t.Error("an empty page must serialise as [], not null")
	}
}

func TestListByCreator_PagesDoNotOverlapOrSkip(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	repo := NewTaskRepository(pool)

	statuses := make([]domain.TaskStatus, 10)
	for i := range statuses {
		statuses[i] = domain.StatusCreated
	}
	seedTasks(t, repo, user.ID, statuses...)

	seen := map[int64]bool{}
	for offset := 0; offset < 10; offset += 3 {
		page, err := repo.ListByCreator(context.Background(), user.ID, domain.TaskFilter{
			Page: domain.PageRequest{Limit: 3, Offset: offset},
		})
		if err != nil {
			t.Fatalf("offset %d: %v", offset, err)
		}
		for _, task := range page.Items {
			if seen[task.ID] {
				t.Errorf("task %d appeared on more than one page", task.ID)
			}
			seen[task.ID] = true
		}
	}
	if len(seen) != 10 {
		t.Errorf("paging through returned %d distinct tasks, want 10", len(seen))
	}
}

func TestListByCreator_FiltersByStatus(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	other := seedUser(t, pool, "bob")
	repo := NewTaskRepository(pool)

	seedTasks(t, repo, user.ID,
		domain.StatusCreated, domain.StatusInProgress, domain.StatusInProgress, domain.StatusCompleted)
	seedTasks(t, repo, other.ID, domain.StatusInProgress)

	inProgress := domain.StatusInProgress
	page, err := repo.ListByCreator(context.Background(), user.ID, domain.TaskFilter{
		Status: &inProgress,
		Page:   domain.PageRequest{Limit: 10},
	})
	if err != nil {
		t.Fatalf("ListByCreator(): %v", err)
	}
	if page.Total != 2 {
		t.Errorf("Total = %d, want 2: the count must respect the filter and the owner", page.Total)
	}
	for _, task := range page.Items {
		if task.Status != inProgress || task.CreatorID != user.ID {
			t.Errorf("unexpected task in the filtered page: %+v", task)
		}
	}
}

func TestListByCreator_SortsByRequestedField(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	repo := NewTaskRepository(pool)

	ctx := context.Background()
	for _, title := range []string{"c", "a", "b"} {
		if _, err := repo.Create(ctx, domain.Task{Title: title, CreatorID: user.ID, Status: domain.StatusCreated}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		time.Sleep(time.Millisecond)
	}

	asc, err := repo.ListByCreator(ctx, user.ID, domain.TaskFilter{
		Sort: domain.SortByTitle, Order: domain.OrderAsc, Page: domain.PageRequest{Limit: 10},
	})
	if err != nil {
		t.Fatalf("ListByCreator(): %v", err)
	}
	var titles []string
	for _, task := range asc.Items {
		titles = append(titles, task.Title)
	}
	if strings.Join(titles, ",") != "a,b,c" {
		t.Errorf("ascending by title = %v, want [a b c]", titles)
	}

	desc, err := repo.ListByCreator(ctx, user.ID, domain.TaskFilter{
		Sort: domain.SortByTitle, Order: domain.OrderDesc, Page: domain.PageRequest{Limit: 10},
	})
	if err != nil {
		t.Fatalf("ListByCreator(): %v", err)
	}
	titles = nil
	for _, task := range desc.Items {
		titles = append(titles, task.Title)
	}
	if strings.Join(titles, ",") != "c,b,a" {
		t.Errorf("descending by title = %v, want [c b a]", titles)
	}
}

func TestListUsers_Paginates(t *testing.T) {
	pool := setupTestPool(t)
	for _, name := range []string{"alice", "bob", "carol", "dave"} {
		seedUser(t, pool, name)
	}
	repo := NewUserRepository(pool)

	page, err := repo.List(context.Background(), domain.PageRequest{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if page.Total != 4 {
		t.Errorf("Total = %d, want 4", page.Total)
	}
	if len(page.Items) != 2 {
		t.Errorf("items = %d, want 2", len(page.Items))
	}
	if page.Items[0].Username != "bob" {
		t.Errorf("first item = %q, want bob (offset 1 of a stable order)", page.Items[0].Username)
	}
}

func TestUsernameUniquenessIsCaseInsensitive(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	if _, err := repo.Create(ctx, domain.User{Username: "Alice", Email: "a@example.com", PasswordHash: "h"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := repo.Create(ctx, domain.User{Username: "alice", Email: "other@example.com", PasswordHash: "h"})
	if !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("creating a name differing only by case error = %v, want apperr.ErrConflict", err)
	}
}

func TestGetByUsernameIgnoresCase(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	created, err := repo.Create(ctx, domain.User{Username: "Alice", Email: "a@example.com", PasswordHash: "h"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, name := range []string{"alice", "ALICE", "AlIcE"} {
		found, err := repo.GetByUsername(ctx, name)
		if err != nil {
			t.Errorf("GetByUsername(%q): %v", name, err)
			continue
		}
		if found.ID != created.ID {
			t.Errorf("GetByUsername(%q) returned id %d, want %d", name, found.ID, created.ID)
		}
		if found.Username != "Alice" {
			t.Errorf("stored spelling should be preserved, got %q", found.Username)
		}
	}
}

func TestRefreshTokenRepository_Lifecycle(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	repo := NewRefreshTokenRepository(pool)
	ctx := context.Background()

	created, err := repo.Create(ctx, domain.RefreshToken{
		UserID:    user.ID,
		TokenHash: "hash-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if created.RevokedAt != nil {
		t.Error("a new token must not be revoked")
	}

	found, err := repo.GetByHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("GetByHash(): %v", err)
	}
	if !found.IsUsable(time.Now()) {
		t.Error("a fresh token should be usable")
	}

	if err := repo.Revoke(ctx, "hash-1"); err != nil {
		t.Fatalf("Revoke(): %v", err)
	}
	revoked, _ := repo.GetByHash(ctx, "hash-1")
	if revoked.RevokedAt == nil || revoked.IsUsable(time.Now()) {
		t.Error("the token should be revoked and unusable")
	}

	if err := repo.Revoke(ctx, "hash-1"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("revoking twice error = %v, want apperr.ErrNotFound", err)
	}
	if _, err := repo.GetByHash(ctx, "never-issued"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown hash error = %v, want apperr.ErrNotFound", err)
	}
}

func TestRefreshTokenRepository_RevokeAllAndCleanup(t *testing.T) {
	pool := setupTestPool(t)
	alice := seedUser(t, pool, "alice")
	bob := seedUser(t, pool, "bob")
	repo := NewRefreshTokenRepository(pool)
	ctx := context.Background()

	for _, hash := range []string{"a1", "a2"} {
		repo.Create(ctx, domain.RefreshToken{UserID: alice.ID, TokenHash: hash, ExpiresAt: time.Now().Add(time.Hour)})
	}
	repo.Create(ctx, domain.RefreshToken{UserID: bob.ID, TokenHash: "b1", ExpiresAt: time.Now().Add(time.Hour)})

	if err := repo.RevokeAllForUser(ctx, alice.ID); err != nil {
		t.Fatalf("RevokeAllForUser(): %v", err)
	}
	for _, hash := range []string{"a1", "a2"} {
		token, _ := repo.GetByHash(ctx, hash)
		if token.RevokedAt == nil {
			t.Errorf("token %s should be revoked", hash)
		}
	}
	other, _ := repo.GetByHash(ctx, "b1")
	if other.RevokedAt != nil {
		t.Error("another user's session must not be revoked")
	}

	repo.Create(ctx, domain.RefreshToken{UserID: bob.ID, TokenHash: "expired", ExpiresAt: time.Now().Add(-time.Hour)})
	removed, err := repo.DeleteExpired(ctx, time.Now())
	if err != nil {
		t.Fatalf("DeleteExpired(): %v", err)
	}
	if removed != 1 {
		t.Errorf("DeleteExpired removed %d rows, want 1", removed)
	}
}

func TestRefreshTokensAreRemovedWithTheirUser(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	tokens := NewRefreshTokenRepository(pool)
	users := NewUserRepository(pool)
	ctx := context.Background()

	tokens.Create(ctx, domain.RefreshToken{UserID: user.ID, TokenHash: "h", ExpiresAt: time.Now().Add(time.Hour)})

	if err := users.Delete(ctx, user.ID); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if _, err := tokens.GetByHash(ctx, "h"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("sessions should be cascaded away with the account, got: %v", err)
	}
}
