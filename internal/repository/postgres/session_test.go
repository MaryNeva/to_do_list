//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"to-do-list/internal/apperr"
	"to-do-list/internal/auth/password"
	"to-do-list/internal/auth/token"
	"to-do-list/internal/domain"
	"to-do-list/internal/usecase"
)

type sessionFixture struct {
	pool    *pgxpool.Pool
	users   *UserRepository
	refresh *RefreshTokenRepository
	auth    *usecase.AuthUseCase
	userUC  *usecase.UserUseCase
	hasher  *password.Hasher
	user    domain.User
}

func newSessionFixture(t *testing.T) sessionFixture {
	t.Helper()
	pool := setupTestPool(t)

	users := NewUserRepository(pool)
	refresh := NewRefreshTokenRepository(pool)

	hasher, err := password.NewHasher(bcrypt.MinCost)
	if err != nil {
		t.Fatalf("NewHasher(): %v", err)
	}
	hash, _ := hasher.Hash("s3cret-pass")
	user, err := users.Create(context.Background(), domain.User{
		Username: "alice", Email: "alice@example.com", PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	tokenSvc, err := token.NewService("0123456789abcdef0123456789abcdef", time.Hour, "to-do-list", 32)
	if err != nil {
		t.Fatalf("token.NewService(): %v", err)
	}

	return sessionFixture{
		pool:    pool,
		users:   users,
		refresh: refresh,
		hasher:  hasher,
		user:    user,
		auth: usecase.NewAuthUseCase(users, tokenSvc, refresh, token.NewIssuer(), hasher, usecase.AuthConfig{
			Timeout: 10 * time.Second, MinUsernameLength: 3, MaxUsernameLength: 50,
			MinPasswordLength: 8, RefreshTTL: 720 * time.Hour,
		}, testLogger()),
		userUC: usecase.NewUserUseCase(users, hasher, usecase.UserConfig{
			Timeout: 10 * time.Second, MinUsernameLength: 3, MaxUsernameLength: 50,
			MinPasswordLength: 8, DefaultPageSize: 20, MaxPageSize: 100,
		}, testLogger()),
	}
}

func (f sessionFixture) login(t *testing.T) domain.Tokens {
	t.Helper()
	tokens, _, err := f.auth.Login(context.Background(), "alice", "s3cret-pass")
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}
	return tokens
}

func (f sessionFixture) activeSessions(t *testing.T) int {
	t.Helper()
	var count int
	err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()`,
		f.user.ID).Scan(&count)
	if err != nil {
		t.Fatalf("count active sessions: %v", err)
	}
	return count
}

type barrierRefreshRepo struct {
	domain.RefreshTokenRepository
	beforeRotate func()
}

func (r *barrierRefreshRepo) Rotate(ctx context.Context, presentedHash string, replacement domain.RefreshToken, now time.Time) (domain.RotateResult, error) {
	if r.beforeRotate != nil {
		r.beforeRotate()
	}
	return r.RefreshTokenRepository.Rotate(ctx, presentedHash, replacement, now)
}

func TestConcurrentRefresh_OnlyOneCallerConsumesTheToken(t *testing.T) {
	f := newSessionFixture(t)
	tokens := f.login(t)

	const callers = 6
	var ready sync.WaitGroup
	ready.Add(callers)
	barrier := &barrierRefreshRepo{RefreshTokenRepository: f.refresh, beforeRotate: func() {
		ready.Done()
		ready.Wait()
	}}

	tokenSvc, _ := token.NewService("0123456789abcdef0123456789abcdef", time.Hour, "to-do-list", 32)
	uc := usecase.NewAuthUseCase(f.users, tokenSvc, barrier, token.NewIssuer(), f.hasher, usecase.AuthConfig{
		Timeout: 10 * time.Second, MinUsernameLength: 3, MaxUsernameLength: 50,
		MinPasswordLength: 8, RefreshTTL: 720 * time.Hour,
	}, testLogger())

	var done sync.WaitGroup
	done.Add(callers)
	results := make([]error, callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer done.Done()
			_, results[i] = uc.Refresh(context.Background(), tokens.RefreshToken)
		}(i)
	}
	done.Wait()

	won := 0
	for _, err := range results {
		if err == nil {
			won++
			continue
		}
		if !errors.Is(err, apperr.ErrUnauthorized) {
			t.Errorf("a losing caller got %v, want apperr.ErrUnauthorized", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d callers exchanged the same refresh token, want exactly 1", won)
	}

	if active := f.activeSessions(t); active != 0 {
		t.Errorf("%d sessions are still active after a detected replay, want 0", active)
	}
}

func TestRefresh_FailedInsertLeavesThePresentedTokenUsable(t *testing.T) {
	f := newSessionFixture(t)
	ctx := context.Background()
	tokens := f.login(t)

	taken, err := f.refresh.Create(ctx, domain.RefreshToken{
		UserID: f.user.ID, TokenHash: "already-taken", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed colliding token: %v", err)
	}

	presentedHash := token.HashRefreshToken(tokens.RefreshToken)
	_, err = f.refresh.Rotate(ctx, presentedHash, domain.RefreshToken{
		TokenHash: taken.TokenHash, ExpiresAt: time.Now().Add(time.Hour),
	}, time.Now())
	if err == nil {
		t.Fatal("rotating onto a hash that already exists should fail")
	}

	stored, err := f.refresh.GetByHash(ctx, presentedHash)
	if err != nil {
		t.Fatalf("GetByHash(): %v", err)
	}
	if stored.RevokedAt != nil {
		t.Error("the presented token was consumed even though the rotation failed")
	}

	if _, err := f.auth.Refresh(ctx, tokens.RefreshToken); err != nil {
		t.Errorf("the session should still be usable after a failed rotation: %v", err)
	}
}

func TestRotationCrossingRevocation_NoLiveDescendantSurvives(t *testing.T) {
	f := newSessionFixture(t)
	tokens := f.login(t)

	var bothInPosition sync.WaitGroup
	bothInPosition.Add(2)

	barrier := &barrierRefreshRepo{RefreshTokenRepository: f.refresh, beforeRotate: func() {
		bothInPosition.Done()
		bothInPosition.Wait()
	}}
	tokenSvc, _ := token.NewService("0123456789abcdef0123456789abcdef", time.Hour, "to-do-list", 32)
	uc := usecase.NewAuthUseCase(f.users, tokenSvc, barrier, token.NewIssuer(), f.hasher, usecase.AuthConfig{
		Timeout: 10 * time.Second, MinUsernameLength: 3, MaxUsernameLength: 50,
		MinPasswordLength: 8, RefreshTTL: 720 * time.Hour,
	}, testLogger())

	var done sync.WaitGroup
	done.Add(2)

	var rotateErr, revokeErr error
	go func() {
		defer done.Done()
		_, rotateErr = uc.Refresh(context.Background(), tokens.RefreshToken)
	}()
	go func() {
		defer done.Done()
		bothInPosition.Done()
		bothInPosition.Wait()
		revokeErr = f.refresh.RevokeAllForUser(context.Background(), f.user.ID)
	}()
	done.Wait()

	if revokeErr != nil {
		t.Fatalf("RevokeAllForUser(): %v", revokeErr)
	}

	active := f.activeSessions(t)
	t.Logf("rotation error: %v; active sessions afterwards: %d", rotateErr, active)
	if active != 0 {
		t.Errorf("%d sessions survived a revocation that ran alongside a rotation, want 0", active)
	}
}

func TestPasswordChange_EndsExistingSessions(t *testing.T) {
	f := newSessionFixture(t)
	ctx := context.Background()
	tokens := f.login(t)

	if _, err := f.userUC.Update(ctx, f.user.ID, "", "", "brand-new-password"); err != nil {
		t.Fatalf("change password: %v", err)
	}

	if _, err := f.auth.Refresh(ctx, tokens.RefreshToken); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("refreshing with a token issued before the password change: %v, want apperr.ErrUnauthorized", err)
	}

	if active := f.activeSessions(t); active != 0 {
		t.Errorf("%d sessions outlived the password change, want 0", active)
	}

	if _, _, err := f.auth.Login(ctx, "alice", "brand-new-password"); err != nil {
		t.Errorf("logging in with the new password should work: %v", err)
	}
}

func TestPasswordChangeCrossingRotation_NoSessionOutlivesTheChange(t *testing.T) {
	f := newSessionFixture(t)
	tokens := f.login(t)

	var bothInPosition sync.WaitGroup
	bothInPosition.Add(2)

	barrier := &barrierRefreshRepo{RefreshTokenRepository: f.refresh, beforeRotate: func() {
		bothInPosition.Done()
		bothInPosition.Wait()
	}}
	tokenSvc, _ := token.NewService("0123456789abcdef0123456789abcdef", time.Hour, "to-do-list", 32)
	uc := usecase.NewAuthUseCase(f.users, tokenSvc, barrier, token.NewIssuer(), f.hasher, usecase.AuthConfig{
		Timeout: 10 * time.Second, MinUsernameLength: 3, MaxUsernameLength: 50,
		MinPasswordLength: 8, RefreshTTL: 720 * time.Hour,
	}, testLogger())

	var done sync.WaitGroup
	done.Add(2)

	var rotated domain.Tokens
	var rotateErr, changeErr error
	go func() {
		defer done.Done()
		rotated, rotateErr = uc.Refresh(context.Background(), tokens.RefreshToken)
	}()
	go func() {
		defer done.Done()
		bothInPosition.Done()
		bothInPosition.Wait()
		_, changeErr = f.userUC.Update(context.Background(), f.user.ID, "", "", "brand-new-password")
	}()
	done.Wait()

	if changeErr != nil {
		t.Fatalf("change password: %v", changeErr)
	}

	active := f.activeSessions(t)
	t.Logf("rotation error: %v; active sessions after the password change: %d", rotateErr, active)
	if active != 0 {
		t.Errorf("%d sessions survived the password change, want 0", active)
	}

	if rotateErr == nil {
		if _, err := f.auth.Refresh(context.Background(), rotated.RefreshToken); !errors.Is(err, apperr.ErrUnauthorized) {
			t.Errorf("a token handed out by the racing rotation is still usable: %v", err)
		}
	}
}

func lockUserRow(t *testing.T, f sessionFixture) (release func(revoke bool)) {
	t.Helper()
	ctx := context.Background()

	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	var id int64
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, f.user.ID).Scan(&id); err != nil {
		t.Fatalf("lock user row: %v", err)
	}

	return func(revoke bool) {
		if revoke {
			if _, err := tx.Exec(ctx,
				`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
				f.user.ID); err != nil {
				t.Errorf("revoke inside the held transaction: %v", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("commit: %v", err)
		}
	}
}

func TestRotate_WaitsForWhoeverHoldsTheUserRow(t *testing.T) {
	f := newSessionFixture(t)
	tokens := f.login(t)

	release := lockUserRow(t, f)

	result := make(chan error, 1)
	go func() {
		_, err := f.auth.Refresh(context.Background(), tokens.RefreshToken)
		result <- err
	}()

	select {
	case err := <-result:
		release(false)
		t.Fatalf("the rotation finished (err=%v) while the user row was locked: it never took the lock", err)
	case <-time.After(500 * time.Millisecond):
	}

	release(true)

	err := <-result
	if !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("rotation after the revocation committed: %v, want apperr.ErrUnauthorized", err)
	}
	if active := f.activeSessions(t); active != 0 {
		t.Errorf("%d sessions survived, want 0: the rotation slipped a replacement past the revocation", active)
	}
}

func TestPasswordChange_WaitsForWhoeverHoldsTheUserRow(t *testing.T) {
	f := newSessionFixture(t)
	f.login(t)

	release := lockUserRow(t, f)

	result := make(chan error, 1)
	go func() {
		_, err := f.userUC.Update(context.Background(), f.user.ID, "", "", "brand-new-password")
		result <- err
	}()

	select {
	case err := <-result:
		release(false)
		t.Fatalf("the password change finished (err=%v) while the user row was locked", err)
	case <-time.After(500 * time.Millisecond):
	}

	release(false)

	if err := <-result; err != nil {
		t.Fatalf("change password: %v", err)
	}
	if active := f.activeSessions(t); active != 0 {
		t.Errorf("%d sessions survived the password change, want 0", active)
	}
}

func TestPasswordChange_FailedUpdateKeepsSessions(t *testing.T) {
	f := newSessionFixture(t)
	ctx := context.Background()
	f.login(t)

	taken, err := f.hasher.Hash("another-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := f.users.Create(ctx, domain.User{
		Username: "bob", Email: "bob@example.com", PasswordHash: taken,
	}); err != nil {
		t.Fatalf("seed second user: %v", err)
	}

	before := f.activeSessions(t)
	if before == 0 {
		t.Fatal("the fixture should start with one active session")
	}

	_, err = f.userUC.Update(ctx, f.user.ID, "bob", "", "brand-new-password")
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("Update() error = %v, want apperr.ErrConflict", err)
	}

	if after := f.activeSessions(t); after != before {
		t.Errorf("%d sessions active after a rejected update, want the original %d", after, before)
	}

	if _, _, err := f.auth.Login(ctx, "alice", "s3cret-pass"); err != nil {
		t.Errorf("the old password must still work after a rejected change: %v", err)
	}
}
