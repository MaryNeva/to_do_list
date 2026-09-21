//go:build integration

package postgres

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

// applyMigrations applies migrations/ so the tests use the real schema.
func applyMigrations(t *testing.T, dsn string) {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the migrations directory")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations")

	m, err := migrate.New("file://"+path, dsn)
	if err != nil {
		t.Fatalf("initialise migrator: %v", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("apply migrations: %v", err)
	}
}

func testPoolConfig() PoolConfig {
	return PoolConfig{
		MaxConns:       8,
		MinConns:       1,
		MaxConnLife:    time.Hour,
		MaxConnIdle:    30 * time.Minute,
		ConnectTimeout: 5 * time.Second,
	}
}

func testDSN(t *testing.T) string {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL is not set, but CI is: the integration job must run against a real database, not skip")
		}
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}

	requireTestDatabase(t, dsn)
	return dsn
}

// requireTestDatabase refuses databases whose name does not end in "_test",
// because the tests truncate every table. TEST_DATABASE_ALLOW_ANY_NAME=1 overrides.
func requireTestDatabase(t *testing.T, dsn string) {
	t.Helper()

	if os.Getenv("TEST_DATABASE_ALLOW_ANY_NAME") == "1" {
		return
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL is not a URL: %v", err)
	}

	// Suffix, not substring: "latest" contains "test".
	name := strings.TrimPrefix(parsed.Path, "/")
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("TEST_DATABASE_URL points at database %q, and these tests TRUNCATE every table. "+
			"Use a database whose name ends in _test (for example %s_test), "+
			"or set TEST_DATABASE_ALLOW_ANY_NAME=1 if you really mean this one.", name, name)
	}
}

func setupTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := testDSN(t)

	applyMigrations(t, dsn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, dsn, testPoolConfig())
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}

	if _, err := pool.Exec(ctx, `TRUNCATE refresh_tokens, tasks, users RESTART IDENTITY CASCADE`); err != nil {
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
	if _, err := repo.Create(ctx, domain.Task{Title: "alice-1", CreatorID: alice.ID, Status: domain.StatusCreated}); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if _, err := repo.Create(ctx, domain.Task{Title: "alice-2", CreatorID: alice.ID, Status: domain.StatusCreated}); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if _, err := repo.Create(ctx, domain.Task{Title: "bob-1", CreatorID: bob.ID, Status: domain.StatusCreated}); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	page, err := repo.ListByCreator(ctx, alice.ID, domain.TaskFilter{Page: domain.PageRequest{Limit: 10}})
	if err != nil {
		t.Fatalf("ListByCreator() unexpected error: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("ListByCreator() returned %d tasks, want 2", len(page.Items))
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

	users, err := repo.List(context.Background(), domain.PageRequest{Limit: 10})
	if err != nil {
		t.Fatalf("List() on an empty table unexpected error: %v", err)
	}
	if len(users.Items) != 0 {
		t.Fatalf("List() = %d users, want 0", len(users.Items))
	}
}

func TestTaskRepository_UpdateWritesOnlyWhatWasSent(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	repo := NewTaskRepository(pool)
	ctx := context.Background()

	for _, tc := range []struct {
		name            string
		update          domain.TaskUpdate
		wantTitle       string
		wantDescription string
		wantStatus      domain.TaskStatus
	}{
		{
			name:      "title alone leaves the description and the status",
			update:    domain.TaskUpdate{Title: strPtr("renamed")},
			wantTitle: "renamed", wantDescription: "original", wantStatus: domain.StatusInProgress,
		},
		{
			name:      "an empty description really clears the column",
			update:    domain.TaskUpdate{Description: strPtr("")},
			wantTitle: "original title", wantDescription: "", wantStatus: domain.StatusInProgress,
		},
		{
			name:      "status alone",
			update:    domain.TaskUpdate{Status: taskStatusPtr(domain.StatusCompleted)},
			wantTitle: "original title", wantDescription: "original", wantStatus: domain.StatusCompleted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task, err := repo.Create(ctx, domain.Task{
				Title: "original title", Description: "original",
				Status: domain.StatusInProgress, CreatorID: user.ID,
			})
			if err != nil {
				t.Fatalf("Create(): %v", err)
			}

			if _, err := repo.Update(ctx, task.ID, user.ID, tc.update); err != nil {
				t.Fatalf("Update(): %v", err)
			}

			stored, err := repo.GetByID(ctx, task.ID)
			if err != nil {
				t.Fatalf("GetByID(): %v", err)
			}
			if stored.Title != tc.wantTitle || stored.Description != tc.wantDescription || stored.Status != tc.wantStatus {
				t.Errorf("stored = {%q, %q, %q}, want {%q, %q, %q}",
					stored.Title, stored.Description, stored.Status,
					tc.wantTitle, tc.wantDescription, tc.wantStatus)
			}
		})
	}
}

func TestTaskRepository_UpdateForeignTaskIsNotFound(t *testing.T) {
	pool := setupTestPool(t)
	alice := seedUser(t, pool, "alice")
	bob := seedUser(t, pool, "bob")
	repo := NewTaskRepository(pool)
	ctx := context.Background()

	task, err := repo.Create(ctx, domain.Task{Title: "alice's", Status: domain.StatusCreated, CreatorID: alice.ID})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	if _, err := repo.Update(ctx, task.ID, bob.ID, domain.TaskUpdate{Title: strPtr("bob's now")}); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("Update() by a stranger error = %v, want apperr.ErrNotFound", err)
	}

	stored, err := repo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetByID(): %v", err)
	}
	if stored.Title != "alice's" {
		t.Errorf("Title = %q, want it untouched", stored.Title)
	}
}

func TestConcurrent_TwoPartialUpdatesBothSurvive(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	repo := NewTaskRepository(pool)
	ctx := context.Background()

	task, err := repo.Create(ctx, domain.Task{
		Title: "before", Description: "before", Status: domain.StatusCreated, CreatorID: user.ID,
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)

	for _, update := range []domain.TaskUpdate{
		{Title: strPtr("title from A")},
		{Description: strPtr("description from B")},
	} {
		wg.Add(1)
		go func(update domain.TaskUpdate) {
			defer wg.Done()
			<-start
			if _, err := repo.Update(ctx, task.ID, user.ID, update); err != nil {
				errs <- err
			}
		}(update)
	}

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Update(): %v", err)
	}

	stored, err := repo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetByID(): %v", err)
	}
	if stored.Title != "title from A" || stored.Description != "description from B" {
		t.Errorf("stored = {%q, %q}, want both changes to have survived", stored.Title, stored.Description)
	}
}

func strPtr(s string) *string { return &s }

func taskStatusPtr(s domain.TaskStatus) *domain.TaskStatus { return &s }

func TestNewPool_AppliesTheConfiguredBounds(t *testing.T) {
	dsn := testDSN(t)

	want := PoolConfig{
		MaxConns:       3,
		MinConns:       1,
		MaxConnLife:    17 * time.Minute,
		MaxConnIdle:    5 * time.Minute,
		ConnectTimeout: 4 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, dsn, want)
	if err != nil {
		t.Fatalf("NewPool(): %v", err)
	}
	defer pool.Close()

	got := pool.Config()
	switch {
	case got.MaxConns != want.MaxConns:
		t.Errorf("MaxConns = %d, want %d", got.MaxConns, want.MaxConns)
	case got.MinConns != want.MinConns:
		t.Errorf("MinConns = %d, want %d", got.MinConns, want.MinConns)
	case got.MaxConnLifetime != want.MaxConnLife:
		t.Errorf("MaxConnLifetime = %s, want %s", got.MaxConnLifetime, want.MaxConnLife)
	case got.MaxConnIdleTime != want.MaxConnIdle:
		t.Errorf("MaxConnIdleTime = %s, want %s", got.MaxConnIdleTime, want.MaxConnIdle)
	case got.ConnConfig.ConnectTimeout != want.ConnectTimeout:
		t.Errorf("ConnectTimeout = %s, want %s", got.ConnConfig.ConnectTimeout, want.ConnectTimeout)
	}

	if stat := pool.Stat(); stat.MaxConns() != want.MaxConns {
		t.Errorf("the live pool reports MaxConns = %d, want %d", stat.MaxConns(), want.MaxConns)
	}
}

func TestNewPool_QueriesWaitForTheCeilingRatherThanExceedIt(t *testing.T) {
	dsn := testDSN(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, dsn, PoolConfig{
		MaxConns: 2, MinConns: 1,
		MaxConnLife: time.Hour, MaxConnIdle: time.Minute, ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPool(): %v", err)
	}
	defer pool.Close()

	held := make([]*pgxpool.Conn, 0, 2)
	for i := 0; i < 2; i++ {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire %d: %v", i+1, err)
		}
		held = append(held, conn)
	}

	full, cancelFull := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancelFull()
	if _, err := pool.Acquire(full); err == nil {
		t.Error("a third connection was handed out although the pool allows two")
	}

	held[0].Release()
	freed, cancelFreed := context.WithTimeout(ctx, 5*time.Second)
	defer cancelFreed()
	conn, err := pool.Acquire(freed)
	if err != nil {
		t.Fatalf("acquiring a released connection: %v", err)
	}
	conn.Release()
	held[1].Release()
}
