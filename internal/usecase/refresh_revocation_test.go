package usecase

import (
	"context"
	"errors"
	"testing"

	"to-do-list/internal/apperr"
)

// A logged-out token from an old device must not end the user's other sessions.
func TestAuthUseCase_Refresh_ALoggedOutTokenIsRejectedWithoutRevokingOthers(t *testing.T) {
	metrics := newFakeMetrics()
	uc, repo, _, refresh := newAuthUseCaseWithRefresh(t, "", "", WithMetrics(metrics))
	ctx := context.Background()

	if _, err := uc.Register(ctx, "mary", "mary@example.com", "password123"); err != nil {
		t.Fatalf("Register(): %v", err)
	}
	oldDevice, _, err := uc.Login(ctx, "mary", "password123")
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}
	newDevice, _, err := uc.Login(ctx, "mary", "password123")
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}

	if err := uc.Logout(ctx, oldDevice.RefreshToken); err != nil {
		t.Fatalf("Logout(): %v", err)
	}

	if _, err := uc.Refresh(ctx, oldDevice.RefreshToken); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("Refresh() with a logged-out token = %v, want apperr.ErrUnauthorized", err)
	}

	var userID int64
	for id := range repo.users {
		userID = id
	}
	if live := refresh.liveSessionsOf(userID); live != 1 {
		t.Fatalf("%d live sessions, want 1: the other device was logged out", live)
	}
	if _, err := uc.Refresh(ctx, newDevice.RefreshToken); err != nil {
		t.Errorf("the other device can no longer refresh: %v", err)
	}
	if got := metrics.count(metrics.rotation, OutcomeRevoked); got != 1 {
		t.Errorf("rotation outcome %q recorded %d times, want 1", OutcomeRevoked, got)
	}
	if got := metrics.count(metrics.revoked, ReasonTokenReuse); got != 0 {
		t.Errorf("recorded %d sessions revoked for reuse, want 0", got)
	}
}

// A replayed rotated token is still treated as reuse.
func TestAuthUseCase_Refresh_ARotatedTokenIsStillReuse(t *testing.T) {
	metrics := newFakeMetrics()
	uc, _, _, _ := newAuthUseCaseWithRefresh(t, "", "", WithMetrics(metrics))
	ctx := context.Background()

	if _, err := uc.Register(ctx, "mary", "mary@example.com", "password123"); err != nil {
		t.Fatalf("Register(): %v", err)
	}
	tokens, _, err := uc.Login(ctx, "mary", "password123")
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}
	if _, err := uc.Refresh(ctx, tokens.RefreshToken); err != nil {
		t.Fatalf("first Refresh(): %v", err)
	}
	if _, err := uc.Refresh(ctx, tokens.RefreshToken); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("replay = %v, want apperr.ErrUnauthorized", err)
	}
	if got := metrics.count(metrics.rotation, OutcomeReuse); got != 1 {
		t.Errorf("reuse recorded %d times, want 1", got)
	}
}

// A storage failure while rotating (such as a hash collision) is an internal
// error, never a 409 or a 401.
func TestAuthUseCase_Refresh_AStorageFailureIsInternal(t *testing.T) {
	uc, _, _, refresh := newAuthUseCaseWithRefresh(t, "", "")
	refresh.rotateErr = errors.New("postgres: scan rotated refresh token: duplicate key")

	_, err := uc.Refresh(context.Background(), "some-token")
	if err == nil {
		t.Fatal("Refresh() succeeded")
	}
	for _, sentinel := range []error{apperr.ErrConflict, apperr.ErrUnauthorized, apperr.ErrTokenRevoked} {
		if errors.Is(err, sentinel) {
			t.Errorf("Refresh() error %v wraps %v; it must be an internal error", err, sentinel)
		}
	}
}
