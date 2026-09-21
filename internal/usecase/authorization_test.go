package usecase

import (
	"context"
	"errors"
	"testing"
	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

func TestUserAuthorizationBeforeAnyDependency(t *testing.T) {
	for _, actor := range []domain.Claims{{}, {UserID: -1}, {UserID: 2}} {
		t.Run(string(rune(actor.UserID+65)), func(t *testing.T) {
			// Nil dependencies make any accidental repository/hash access fail loudly.
			uc := &UserUseCase{}
			want := apperr.ErrForbidden
			if actor.UserID <= 0 {
				want = apperr.ErrUnauthorized
			}
			_, getErr := uc.Get(context.Background(), actor, 1)
			_, listErr := uc.List(context.Background(), actor, domain.PageRequest{})
			_, updateErr := uc.Update(context.Background(), actor, 1, domain.UserEdit{Password: ptr("new-password")})
			deleteErr := uc.Delete(context.Background(), actor, 1)
			for _, err := range []error{getErr, listErr, updateErr, deleteErr} {
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
			}
		})
	}
}

func TestUserAuthorizationSelfAndAdmin(t *testing.T) {
	for _, admin := range []bool{false, true} {
		uc, repo := newUserUseCaseForTest(t)
		user, err := repo.Create(context.Background(), domain.User{Username: "alice", Email: "alice@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		actor := domain.Claims{UserID: user.ID}
		if admin {
			actor = domain.Claims{IsAdmin: true}
		}
		if _, err := uc.Get(context.Background(), actor, user.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := uc.Update(context.Background(), actor, user.ID, domain.UserEdit{Username: ptr("alice2")}); err != nil {
			t.Fatal(err)
		}
		_, err = uc.List(context.Background(), actor, domain.PageRequest{})
		if admin && err != nil || !admin && !errors.Is(err, apperr.ErrForbidden) {
			t.Fatalf("list: %v", err)
		}
		if err := uc.Delete(context.Background(), actor, user.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTaskAuthorizationBeforeAnyDependency(t *testing.T) {
	// Nil repository: anything that reaches it panics instead of passing.
	uc := &TaskUseCase{}
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		actor domain.Claims
		want  error
	}{
		{"no claims", domain.Claims{}, apperr.ErrUnauthorized},
		{"negative id", domain.Claims{UserID: -1}, apperr.ErrUnauthorized},
		{"bootstrap admin owns no tasks", domain.Claims{IsAdmin: true}, apperr.ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, createErr := uc.Create(ctx, tc.actor, "title", "")
			_, getErr := uc.Get(ctx, tc.actor, 1)
			_, listErr := uc.List(ctx, tc.actor, domain.TaskFilter{})
			_, updateErr := uc.Update(ctx, tc.actor, 1, domain.TaskUpdate{Title: ptr("title")})
			deleteErr := uc.Delete(ctx, tc.actor, 1)
			_, toggleErr := uc.ToggleStatus(ctx, tc.actor, 1)

			for _, err := range []error{createErr, getErr, listErr, updateErr, deleteErr, toggleErr} {
				if !errors.Is(err, tc.want) {
					t.Fatalf("got %v, want %v", err, tc.want)
				}
			}
		})
	}
}

func TestTaskOwnerComesFromClaims(t *testing.T) {
	uc, _ := newTaskUseCaseForTest()
	ctx := context.Background()

	created, err := uc.Create(ctx, domain.Claims{UserID: 7}, "mine", "")
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatorID != 7 {
		t.Fatalf("creator_id = %d, want 7", created.CreatorID)
	}

	if _, err := uc.Get(ctx, domain.Claims{UserID: 8}, created.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("another user read the task: %v", err)
	}
}
