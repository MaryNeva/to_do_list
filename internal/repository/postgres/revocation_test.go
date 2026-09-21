//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

func revokedReason(t *testing.T, f sessionFixture, plain string) *string {
	t.Helper()
	var reason *string
	err := f.pool.QueryRow(context.Background(),
		`SELECT revoked_reason FROM refresh_tokens WHERE token_hash = $1`, sha256Hex(plain)).Scan(&reason)
	if err != nil {
		t.Fatalf("read revoked_reason: %v", err)
	}
	return reason
}

func TestRotate_ALoggedOutTokenIsRevokedNotReused(t *testing.T) {
	f := newSessionFixture(t)
	ctx := context.Background()

	oldDevice := f.login(t)
	newDevice := f.login(t)
	if err := f.auth.Logout(ctx, oldDevice.RefreshToken); err != nil {
		t.Fatalf("Logout(): %v", err)
	}

	result, err := f.refresh.Rotate(ctx, sha256Hex(oldDevice.RefreshToken), domain.RefreshToken{
		TokenHash: "replacement-after-logout", ExpiresAt: time.Now().Add(time.Hour),
	})
	if !errors.Is(err, apperr.ErrTokenRevoked) {
		t.Fatalf("Rotate() of a logged-out token = %v, want apperr.ErrTokenRevoked", err)
	}
	if result.SessionsRevoked != 0 {
		t.Errorf("SessionsRevoked = %d, want 0", result.SessionsRevoked)
	}
	if live := f.activeSessions(t); live != 1 {
		t.Fatalf("%d active sessions, want 1: the other device was logged out", live)
	}
	if _, err := f.auth.Refresh(ctx, newDevice.RefreshToken); err != nil {
		t.Errorf("the other device can no longer refresh: %v", err)
	}
}

// After a password change, a stale token from another device must not end the
// session opened with the new password.
func TestRotate_ATokenFromBeforeAPasswordChangeDoesNotEndNewSessions(t *testing.T) {
	f := newSessionFixture(t)
	ctx := context.Background()

	stale := f.login(t)
	if _, err := f.userUC.Update(ctx, domain.Claims{UserID: f.user.ID}, f.user.ID,
		domain.UserEdit{Password: strPtr("a-brand-new-password")}); err != nil {
		t.Fatalf("change password: %v", err)
	}
	fresh, _, err := f.auth.Login(ctx, "alice", "a-brand-new-password")
	if err != nil {
		t.Fatalf("Login() with the new password: %v", err)
	}

	if got := revokedReason(t, f, stale.RefreshToken); got == nil || *got != reasonPasswordChange {
		t.Fatalf("revoked_reason = %v, want %q", got, reasonPasswordChange)
	}

	if _, err := f.auth.Refresh(ctx, stale.RefreshToken); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("Refresh() with the stale token = %v, want apperr.ErrUnauthorized", err)
	}
	if _, err := f.auth.Refresh(ctx, fresh.RefreshToken); err != nil {
		t.Errorf("the new session was ended by the stale token: %v", err)
	}
}

func TestRevocationReasonsAreRecorded(t *testing.T) {
	f := newSessionFixture(t)
	ctx := context.Background()

	loggedOut := f.login(t)
	rotated := f.login(t)
	if err := f.auth.Logout(ctx, loggedOut.RefreshToken); err != nil {
		t.Fatalf("Logout(): %v", err)
	}
	next, err := f.auth.Refresh(ctx, rotated.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh(): %v", err)
	}
	// Replaying the rotated token revokes the rest with reason "reuse".
	if _, err := f.auth.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("replay = %v, want apperr.ErrUnauthorized", err)
	}

	for name, tc := range map[string]struct {
		plain, want string
	}{
		"logout":               {loggedOut.RefreshToken, reasonLogout},
		"rotation":             {rotated.RefreshToken, reasonRotated},
		"revoked after replay": {next.RefreshToken, reasonReuse},
	} {
		if got := revokedReason(t, f, tc.plain); got == nil || *got != tc.want {
			t.Errorf("%s: revoked_reason = %v, want %q", name, got, tc.want)
		}
	}
}

// A replacement hash that already exists is an internal failure: no
// ErrConflict (409) and nothing written.
func TestRotate_AHashCollisionIsAnInternalErrorAndWritesNothing(t *testing.T) {
	f := newSessionFixture(t)
	ctx := context.Background()

	presented := f.login(t)
	other := f.login(t)

	_, err := f.refresh.Rotate(ctx, sha256Hex(presented.RefreshToken), domain.RefreshToken{
		TokenHash: sha256Hex(other.RefreshToken), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err == nil {
		t.Fatal("Rotate() with a colliding hash succeeded")
	}
	for _, sentinel := range []error{apperr.ErrConflict, apperr.ErrTokenReuse, apperr.ErrTokenRevoked, apperr.ErrUnauthorized} {
		if errors.Is(err, sentinel) {
			t.Errorf("Rotate() error %v wraps %v, want an internal error", err, sentinel)
		}
	}
	if revokedReason(t, f, presented.RefreshToken) != nil {
		t.Error("the presented token was revoked although the rotation failed")
	}
	if _, err := f.auth.Refresh(ctx, presented.RefreshToken); err != nil {
		t.Errorf("the presented token is no longer usable: %v", err)
	}
}

func TestCreate_AHashCollisionIsAnInternalError(t *testing.T) {
	f := newSessionFixture(t)
	existing := f.login(t)

	_, err := f.refresh.Create(context.Background(), domain.RefreshToken{
		UserID: f.user.ID, TokenHash: sha256Hex(existing.RefreshToken), ExpiresAt: time.Now().Add(time.Hour),
	}, f.user.CredentialsVersion)
	if err == nil {
		t.Fatal("Create() with a colliding hash succeeded")
	}
	if errors.Is(err, apperr.ErrConflict) {
		t.Errorf("Create() error %v wraps apperr.ErrConflict, which means a password change", err)
	}
}

func TestUsers_EmailIsUniqueRegardlessOfCase(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewUserRepository(pool)
	ctx := context.Background()

	if _, err := repo.Create(ctx, domain.User{Username: "mary", Email: "Mary@Example.com", PasswordHash: "h"}); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if _, err := repo.Create(ctx, domain.User{Username: "other", Email: "mary@example.COM", PasswordHash: "h"}); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("Create() with the same email in another case = %v, want apperr.ErrConflict", err)
	}

	bob, err := repo.Create(ctx, domain.User{Username: "bob", Email: "bob@example.com", PasswordHash: "h"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	taken := "MARY@example.com"
	if _, err := repo.Update(ctx, bob.ID, domain.UserUpdate{Email: &taken}); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("Update() to another user's email in another case = %v, want apperr.ErrConflict", err)
	}

	// Changing only the case of one's own email is allowed.
	own := "BOB@example.com"
	if _, err := repo.Update(ctx, bob.ID, domain.UserUpdate{Email: &own}); err != nil {
		t.Errorf("Update() of own email case: %v", err)
	}
}

// rotate -> end the chain -> log in again -> replay the ancestor. The replay
// must be rejected without ending the session opened afterwards.
func TestRotate_AnAncestorOfAnEndedChainDoesNotEndLaterSessions(t *testing.T) {
	for _, tc := range []struct {
		name        string
		end         func(t *testing.T, f sessionFixture, live string)
		newPassword string
	}{
		{
			name: "password change",
			end: func(t *testing.T, f sessionFixture, _ string) {
				if _, err := f.userUC.Update(context.Background(), domain.Claims{UserID: f.user.ID}, f.user.ID,
					domain.UserEdit{Password: strPtr("a-brand-new-password")}); err != nil {
					t.Fatalf("change password: %v", err)
				}
			},
			newPassword: "a-brand-new-password",
		},
		{
			name: "logout",
			end: func(t *testing.T, f sessionFixture, live string) {
				if err := f.auth.Logout(context.Background(), live); err != nil {
					t.Fatalf("Logout(): %v", err)
				}
			},
			newPassword: "s3cret-pass",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSessionFixture(t)
			ctx := context.Background()

			ancestor := f.login(t)
			child, err := f.auth.Refresh(ctx, ancestor.RefreshToken)
			if err != nil {
				t.Fatalf("Refresh(): %v", err)
			}

			tc.end(t, f, child.RefreshToken)

			later, _, err := f.auth.Login(ctx, "alice", tc.newPassword)
			if err != nil {
				t.Fatalf("Login() after ending the chain: %v", err)
			}

			result, err := f.refresh.Rotate(ctx, sha256Hex(ancestor.RefreshToken), domain.RefreshToken{
				TokenHash: "replacement-for-the-ancestor", ExpiresAt: time.Now().Add(time.Hour),
			})
			if !errors.Is(err, apperr.ErrTokenRevoked) {
				t.Fatalf("Rotate() of the ancestor = %v, want apperr.ErrTokenRevoked", err)
			}
			if result.SessionsRevoked != 0 {
				t.Errorf("SessionsRevoked = %d, want 0", result.SessionsRevoked)
			}
			if live := f.activeSessions(t); live != 1 {
				t.Fatalf("%d active sessions, want 1", live)
			}
			if _, err := f.auth.Refresh(ctx, later.RefreshToken); err != nil {
				t.Errorf("the session opened after the chain ended no longer works: %v", err)
			}
		})
	}
}

// While the chain is live, replaying its ancestor is still reuse.
func TestRotate_AnAncestorOfALiveChainIsReuse(t *testing.T) {
	f := newSessionFixture(t)
	ctx := context.Background()

	ancestor := f.login(t)
	if _, err := f.auth.Refresh(ctx, ancestor.RefreshToken); err != nil {
		t.Fatalf("Refresh(): %v", err)
	}
	other := f.login(t)

	result, err := f.refresh.Rotate(ctx, sha256Hex(ancestor.RefreshToken), domain.RefreshToken{
		TokenHash: "replacement-for-the-ancestor", ExpiresAt: time.Now().Add(time.Hour),
	})
	if !errors.Is(err, apperr.ErrTokenReuse) {
		t.Fatalf("Rotate() of the ancestor = %v, want apperr.ErrTokenReuse", err)
	}
	if result.SessionsRevoked != 2 {
		t.Errorf("SessionsRevoked = %d, want 2", result.SessionsRevoked)
	}
	if _, err := f.auth.Refresh(ctx, other.RefreshToken); err == nil {
		t.Error("another session survived a replay of a live chain")
	}
}

// A rotation keeps the family; a login starts a new one.
func TestRotate_TheReplacementJoinsTheFamily(t *testing.T) {
	f := newSessionFixture(t)
	ctx := context.Background()

	first := f.login(t)
	rotated, err := f.auth.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh(): %v", err)
	}
	second := f.login(t)

	family := func(plain string) string {
		var id string
		if err := f.pool.QueryRow(ctx, `SELECT family_id::text FROM refresh_tokens WHERE token_hash = $1`,
			sha256Hex(plain)).Scan(&id); err != nil {
			t.Fatalf("read family_id: %v", err)
		}
		return id
	}
	if family(first.RefreshToken) != family(rotated.RefreshToken) {
		t.Error("the rotated token is not in the same family")
	}
	if family(first.RefreshToken) == family(second.RefreshToken) {
		t.Error("a second login joined the first login's family")
	}
}

// Rows revoked before migration 000005 have no reason; they are ordinary
// revocations and never trigger reuse.
func TestRotate_ALegacyRevokedTokenIsNotReuse(t *testing.T) {
	f := newSessionFixture(t)
	ctx := context.Background()

	other := f.login(t)
	if _, err := f.pool.Exec(ctx, `INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked_at)
		VALUES ($1, 'legacy-hash', now() + interval '1 day', now())`, f.user.ID); err != nil {
		t.Fatalf("insert legacy token: %v", err)
	}

	if _, err := f.refresh.Rotate(ctx, "legacy-hash", domain.RefreshToken{
		TokenHash: "replacement-for-legacy", ExpiresAt: time.Now().Add(time.Hour),
	}); !errors.Is(err, apperr.ErrTokenRevoked) {
		t.Fatalf("Rotate() of a legacy revoked token = %v, want apperr.ErrTokenRevoked", err)
	}
	if _, err := f.auth.Refresh(ctx, other.RefreshToken); err != nil {
		t.Errorf("another session was ended by a legacy token: %v", err)
	}
}
