//go:build integration

package postgres

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"to-do-list/internal/apperr"
	"to-do-list/internal/auth/password"
	"to-do-list/internal/domain"
	"to-do-list/internal/usecase"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testHasher(t *testing.T) *password.Hasher {
	t.Helper()
	h, err := password.NewHasher(bcrypt.MinCost, 2)
	if err != nil {
		t.Fatalf("password.NewHasher(): %v", err)
	}
	return h
}

func TestConcurrent_ProfileUpdateDoesNotLoseOtherFields(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	hasher := testHasher(t)
	oldHash, _ := hasher.Hash(context.Background(), "original-password")
	user, err := repo.Create(ctx, domain.User{Username: "alice", Email: "a@example.com", PasswordHash: oldHash})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := repo.GetByID(ctx, user.ID); err != nil {
		t.Fatalf("A read: %v", err)
	}

	newHash, _ := hasher.Hash(context.Background(), "new-password")
	if _, err := repo.Update(ctx, user.ID, domain.UserUpdate{PasswordHash: &newHash}); err != nil {
		t.Fatalf("B update: %v", err)
	}

	// Simulates a stale read: A read the row, B changed the password, then A
	// writes the email. The update must not restore the old hash.
	newEmail := "new@example.com"
	if _, err := repo.Update(ctx, user.ID, domain.UserUpdate{Email: &newEmail}); err != nil {
		t.Fatalf("A update: %v", err)
	}

	final, err := repo.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	if final.Email != newEmail {
		t.Errorf("Email = %q, want %q", final.Email, newEmail)
	}
	if !hasherMatches(t, final.PasswordHash, "new-password") {
		t.Error("the password change was overwritten by the email change")
	}
}

func TestConcurrent_ProfileUpdatesThroughUseCase(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	hasher := testHasher(t)
	uc := usecase.NewUserUseCase(repo, hasher, usecase.UserConfig{
		Timeout:           5 * time.Second,
		MinUsernameLength: 3,
		MaxUsernameLength: 50,
		MinPasswordLength: 8,
		DefaultPageSize:   20,
		MaxPageSize:       100,
	}, testLogger())

	oldHash, _ := hasher.Hash(context.Background(), "original-password")
	user, err := repo.Create(ctx, domain.User{Username: "alice", Email: "a@example.com", PasswordHash: oldHash})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	errs := make([]error, 2)

	done.Add(2)
	go func() {
		defer done.Done()
		start.Wait()
		_, errs[0] = uc.Update(ctx, domain.Claims{UserID: user.ID}, user.ID, domain.UserEdit{Email: strPtr("changed@example.com")})
	}()
	go func() {
		defer done.Done()
		start.Wait()
		_, errs[1] = uc.Update(ctx, domain.Claims{UserID: user.ID}, user.ID, domain.UserEdit{Password: strPtr("brand-new-password")})
	}()
	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}

	final, _ := repo.GetByID(ctx, user.ID)
	if final.Email != "changed@example.com" {
		t.Errorf("Email = %q, want the concurrently written value", final.Email)
	}
	if !hasherMatches(t, final.PasswordHash, "brand-new-password") {
		t.Error("the concurrent password change was lost")
	}
}

func newTaskUseCaseForIntegration(repo usecase.TaskRepository) *usecase.TaskUseCase {
	return usecase.NewTaskUseCase(repo, usecase.TaskConfig{
		Timeout:              5 * time.Second,
		MaxTitleLength:       200,
		MaxDescriptionLength: 4000,
		DefaultPageSize:      20,
		MaxPageSize:          100,
	}, testLogger())
}

func TestConcurrent_TwoTogglesProduceTwoTransitions(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	repo := NewTaskRepository(pool)
	uc := newTaskUseCaseForIntegration(repo)
	ctx := context.Background()

	const rounds = 20
	for round := 0; round < rounds; round++ {
		task, err := repo.Create(ctx, domain.Task{Title: "t", CreatorID: user.ID, Status: domain.StatusCreated})
		if err != nil {
			t.Fatalf("round %d seed: %v", round, err)
		}

		var start sync.WaitGroup
		var done sync.WaitGroup
		start.Add(1)
		done.Add(2)
		errs := make([]error, 2)
		for i := 0; i < 2; i++ {
			go func(i int) {
				defer done.Done()
				start.Wait()
				_, errs[i] = uc.ToggleStatus(ctx, domain.Claims{UserID: user.ID}, task.ID)
			}(i)
		}
		start.Done()
		done.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d toggle %d: %v", round, i, err)
			}
		}

		final, _ := repo.GetByID(ctx, task.ID)
		if final.Status != domain.StatusCompleted {
			t.Fatalf("round %d: after two toggles Status = %q, want %q (a transition was lost)",
				round, final.Status, domain.StatusCompleted)
		}
	}
}

// N concurrent toggles must advance the status exactly N steps.
func TestConcurrent_ManyTogglesLandOnTheRightStatus(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	repo := NewTaskRepository(pool)
	uc := newTaskUseCaseForIntegration(repo)
	ctx := context.Background()

	task, err := repo.Create(ctx, domain.Task{Title: "t", CreatorID: user.ID, Status: domain.StatusCreated})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	const toggles = 9 // three full cycles: back to "created"
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(toggles)
	errs := make([]error, toggles)
	for i := 0; i < toggles; i++ {
		go func(i int) {
			defer done.Done()
			start.Wait()
			_, errs[i] = uc.ToggleStatus(ctx, domain.Claims{UserID: user.ID}, task.ID)
		}(i)
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("toggle %d: %v", i, err)
		}
	}

	want := domain.StatusCreated
	for i := 0; i < toggles; i++ {
		want = domain.NextStatus(want)
	}

	final, _ := repo.GetByID(ctx, task.ID)
	if final.Status != want {
		t.Errorf("after %d concurrent toggles Status = %q, want %q", toggles, final.Status, want)
	}
}

func TestCompareAndSetStatus_ForeignTaskIsNotFound(t *testing.T) {
	pool := setupTestPool(t)
	alice := seedUser(t, pool, "alice")
	bob := seedUser(t, pool, "bob")
	repo := NewTaskRepository(pool)
	ctx := context.Background()

	task, err := repo.Create(ctx, domain.Task{Title: "alice's", CreatorID: alice.ID, Status: domain.StatusCreated})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err = repo.CompareAndSetStatus(ctx, task.ID, bob.ID, domain.StatusCreated, domain.StatusInProgress)
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("error = %v, want apperr.ErrNotFound", err)
	}

	unchanged, _ := repo.GetByID(ctx, task.ID)
	if unchanged.Status != domain.StatusCreated {
		t.Errorf("a foreign compare-and-set changed the status to %q", unchanged.Status)
	}

	if _, err := repo.CompareAndSetStatus(ctx, 999999, alice.ID, domain.StatusCreated, domain.StatusInProgress); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("missing task error = %v, want apperr.ErrNotFound", err)
	}
}

func TestCompareAndSetStatus_StaleFromIsConflict(t *testing.T) {
	pool := setupTestPool(t)
	user := seedUser(t, pool, "alice")
	repo := NewTaskRepository(pool)
	ctx := context.Background()

	task, err := repo.Create(ctx, domain.Task{Title: "t", CreatorID: user.ID, Status: domain.StatusInProgress})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err = repo.CompareAndSetStatus(ctx, task.ID, user.ID, domain.StatusCreated, domain.StatusInProgress)
	if !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("error = %v, want apperr.ErrConflict", err)
	}
}

func TestUserUpdate_PartialFieldsOnly(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	user, err := repo.Create(ctx, domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "HASH"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	newName := "alice2"
	updated, err := repo.Update(ctx, user.ID, domain.UserUpdate{Username: &newName})
	if err != nil {
		t.Fatalf("Update(): %v", err)
	}
	if updated.Username != newName || updated.Email != "a@example.com" || updated.PasswordHash != "HASH" {
		t.Errorf("partial update changed more than the username: %+v", updated)
	}

	empty, err := repo.Update(ctx, user.ID, domain.UserUpdate{})
	if err != nil {
		t.Fatalf("empty Update(): %v", err)
	}
	if empty.Username != newName || empty.Email != "a@example.com" || empty.PasswordHash != "HASH" {
		t.Errorf("an empty update changed the row: %+v", empty)
	}

	if _, err := repo.Update(ctx, 999999, domain.UserUpdate{Email: &newName}); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("updating a missing user error = %v, want apperr.ErrNotFound", err)
	}
}

func TestUserUpdate_DuplicateUsernameIsConflict(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	if _, err := repo.Create(ctx, domain.User{Username: "taken", Email: "t@example.com", PasswordHash: "h"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	other, err := repo.Create(ctx, domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "h"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	taken := "taken"
	if _, err := repo.Update(ctx, other.ID, domain.UserUpdate{Username: &taken}); !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("error = %v, want apperr.ErrConflict", err)
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
