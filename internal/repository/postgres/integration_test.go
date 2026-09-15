//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

const testSchema = `
CREATE TABLE IF NOT EXISTS users (
	id BIGSERIAL PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	email TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS tasks (
	id BIGSERIAL PRIMARY KEY,
	title TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'created',
	creator_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

func setupTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, dsn, 5*time.Second)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}

	if _, err := pool.Exec(ctx, testSchema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE tasks, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate test tables: %v", err)
	}

	t.Cleanup(pool.Close)

	return pool
}

func seedUser(t *testing.T, pool *pgxpool.Pool, username string) domain.User {
	t.Helper()
	repo := NewUserRepository(pool)
	user, err := repo.Create(context.Background(), domain.User{
		Username:     username,
		Email:        username + "@example.com",
		PasswordHash: "hashed",
	})
	if err != nil {
		t.Fatalf("seedUser: %v", err)
	}
	return user
}

func TestTaskRepository_CreateStoresCreatorID(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	repo := NewTaskRepository(pool)

	task, err := repo.Create(context.Background(), domain.Task{
		Title:       "Buy milk",
		Description: "2%",
		Status:      domain.StatusCreated,
		CreatorID:   user.ID,
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if task.CreatorID != user.ID {
		t.Fatalf("Create() CreatorID = %d, want %d", task.CreatorID, user.ID)
	}

	fetched, err := repo.GetByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetByID() unexpected error: %v", err)
	}
	if fetched.CreatorID != user.ID {
		t.Fatalf("GetByID() CreatorID = %d, want %d", fetched.CreatorID, user.ID)
	}
}

func TestTaskRepository_ListByCreator(t *testing.T) {
	pool := setupTestPool(t)
	alice := seedUser(t, pool, "alice")
	bob := seedUser(t, pool, "bob")
	repo := NewTaskRepository(pool)

	ctx := context.Background()
	if _, err := repo.Create(ctx, domain.Task{Title: "alice-1", CreatorID: alice.ID}); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if _, err := repo.Create(ctx, domain.Task{Title: "alice-2", CreatorID: alice.ID}); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if _, err := repo.Create(ctx, domain.Task{Title: "bob-1", CreatorID: bob.ID}); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	tasks, err := repo.ListByCreator(ctx, alice.ID)
	if err != nil {
		t.Fatalf("ListByCreator() unexpected error: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("ListByCreator() returned %d tasks, want 2", len(tasks))
	}
}

func TestTaskRepository_GetByID_NotFound(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewTaskRepository(pool)

	_, err := repo.GetByID(context.Background(), 999999)
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("GetByID() for a missing task error = %v, want apperr.ErrNotFound", err)
	}
}

func TestTaskRepository_ToggleAndDelete(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	repo := NewTaskRepository(pool)
	ctx := context.Background()

	task, err := repo.Create(ctx, domain.Task{Title: "toggle-me", CreatorID: user.ID, Status: domain.StatusCreated})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	moved, err := repo.CompareAndSetStatus(ctx, task.ID, user.ID, domain.StatusCreated, domain.StatusInProgress)
	if err != nil {
		t.Fatalf("CompareAndSetStatus() unexpected error: %v", err)
	}
	if moved.Status != domain.StatusInProgress {
		t.Fatalf("returned Status = %q, want %q", moved.Status, domain.StatusInProgress)
	}
	got, err := repo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetByID() unexpected error: %v", err)
	}
	if got.Status != domain.StatusInProgress {
		t.Fatalf("Status = %q, want %q", got.Status, domain.StatusInProgress)
	}

	if err := repo.Delete(ctx, task.ID); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if _, err := repo.GetByID(ctx, task.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("GetByID() after Delete() error = %v, want apperr.ErrNotFound", err)
	}
}

func TestUserRepository_DuplicateUsername_ReturnsConflict(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	if _, err := repo.Create(ctx, domain.User{Username: "alice", Email: "a1@example.com", PasswordHash: "h"}); err != nil {
		t.Fatalf("first Create() unexpected error: %v", err)
	}

	_, err := repo.Create(ctx, domain.User{Username: "alice", Email: "a2@example.com", PasswordHash: "h"})
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("Create() with a duplicate username error = %v, want apperr.ErrConflict", err)
	}
}

func TestUserRepository_DuplicateEmail_ReturnsConflict(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	if _, err := repo.Create(ctx, domain.User{Username: "alice", Email: "dup@example.com", PasswordHash: "h"}); err != nil {
		t.Fatalf("first Create() unexpected error: %v", err)
	}

	_, err := repo.Create(ctx, domain.User{Username: "bob", Email: "dup@example.com", PasswordHash: "h"})
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("Create() with a duplicate email error = %v, want apperr.ErrConflict", err)
	}
}

func TestUserRepository_GetByUsername_NotFound(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewUserRepository(pool)

	_, err := repo.GetByUsername(context.Background(), "ghost")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("GetByUsername() for a missing user error = %v, want apperr.ErrNotFound", err)
	}
}

func TestUserRepository_List_EmptyIsNotAnError(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewUserRepository(pool)

	users, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("List() on an empty table unexpected error: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("List() = %d users, want 0", len(users))
	}
}
