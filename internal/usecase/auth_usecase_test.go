package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"to-do-list/internal/apperr"
	"to-do-list/internal/auth/password"
	"to-do-list/internal/domain"
)

// fakeTokenService avoids depending on real JWT/crypto code in usecase-level
// tests; internal/auth/token has its own dedicated test suite for that.
type fakeTokenService struct {
	generateErr error
}

func (f *fakeTokenService) Generate(userID int64, username string, isAdmin bool) (string, time.Time, error) {
	if f.generateErr != nil {
		return "", time.Time{}, f.generateErr
	}
	return fmt.Sprintf("token-for-%d-%s-admin:%v", userID, username, isAdmin), time.Now().Add(time.Hour), nil
}

func (f *fakeTokenService) Parse(tokenString string) (domain.Claims, error) {
	return domain.Claims{}, errors.New("not used in these tests")
}

func newAuthUseCaseForTest(t *testing.T, adminUsername, adminPasswordHash string) (*AuthUseCase, *fakeUserRepo, *fakeTokenService) {
	t.Helper()
	uc, repo, tokens, _ := newAuthUseCaseWithRefresh(t, adminUsername, adminPasswordHash)
	return uc, repo, tokens
}

func newAuthUseCaseWithRefresh(t *testing.T, adminUsername, adminPasswordHash string) (*AuthUseCase, *fakeUserRepo, *fakeTokenService, *fakeRefreshRepo) {
	t.Helper()
	repo := newFakeUserRepo()
	tokens := &fakeTokenService{}
	refresh := newFakeRefreshRepo()
	cfg := AuthConfig{
		AdminUsername:     adminUsername,
		AdminPasswordHash: adminPasswordHash,
		Timeout:           time.Second,
		MinUsernameLength: 3,
		MaxUsernameLength: 50,
		MinPasswordLength: 8,
		RefreshTTL:        720 * time.Hour,
	}
	uc := NewAuthUseCase(repo, tokens, refresh, &fakeIssuer{}, testHasher(t), cfg, silentLogger())
	return uc, repo, tokens, refresh
}

func TestAuthUseCase_Register(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest(t, "", "")

	user, err := uc.Register(context.Background(), "alice", "alice@example.com", "s3cret-pass")
	if err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}
	if user.Username != "alice" {
		t.Errorf("Register() Username = %q, want %q", user.Username, "alice")
	}
	if user.PasswordHash == "s3cret-pass" || user.PasswordHash == "" {
		t.Error("Register() must store a hashed password, not plaintext or empty")
	}
	if !password.Matches(user.PasswordHash, "s3cret-pass") {
		t.Error("Register() stored hash does not verify against the original password")
	}
}

func TestAuthUseCase_Register_DuplicateUsername(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest(t, "", "")

	if _, err := uc.Register(context.Background(), "alice", "alice@example.com", "s3cret-pass"); err != nil {
		t.Fatalf("first Register() unexpected error: %v", err)
	}

	_, err := uc.Register(context.Background(), "alice", "different@example.com", "another-pass")
	if !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("Register() with a duplicate username error = %v, want apperr.ErrConflict", err)
	}
}

func TestAuthUseCase_Register_MissingFields(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest(t, "", "")

	if _, err := uc.Register(context.Background(), "", "a@example.com", "pass1234"); !errors.Is(err, apperr.ErrValidation) {
		t.Errorf("Register() with empty username error = %v, want apperr.ErrValidation", err)
	}
}

func TestAuthUseCase_Login_Success(t *testing.T) {
	uc, repo, _ := newAuthUseCaseForTest(t, "", "")

	hash, _ := testHasher(t).Hash("s3cret-pass")
	repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	tokens, user, err := uc.Login(context.Background(), "alice", "s3cret-pass")
	if err != nil {
		t.Fatalf("Login() unexpected error: %v", err)
	}
	if tokens.AccessToken == "" {
		t.Error("Login() returned an empty token")
	}
	if !tokens.AccessExpiresAt.After(time.Now()) {
		t.Error("Login() expiresAt should be in the future")
	}
	if user.Username != "alice" {
		t.Errorf("Login() Username = %q, want %q", user.Username, "alice")
	}
}

func TestAuthUseCase_Login_WrongPassword(t *testing.T) {
	uc, repo, _ := newAuthUseCaseForTest(t, "", "")
	hash, _ := testHasher(t).Hash("s3cret-pass")
	repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	_, _, err := uc.Login(context.Background(), "alice", "wrong-password")
	if !errors.Is(err, apperr.ErrInvalidCredentials) {
		t.Errorf("Login() with the wrong password error = %v, want apperr.ErrInvalidCredentials", err)
	}
}

func TestAuthUseCase_Login_UnknownUser_SameErrorAsWrongPassword(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest(t, "", "")

	_, _, err := uc.Login(context.Background(), "ghost", "whatever")
	if !errors.Is(err, apperr.ErrInvalidCredentials) {
		t.Errorf("Login() for an unknown user error = %v, want apperr.ErrInvalidCredentials", err)
	}
}

func TestAuthUseCase_Login_AdminBootstrap(t *testing.T) {
	adminHash, _ := testHasher(t).Hash("admin-pass")
	uc, _, _ := newAuthUseCaseForTest(t, "admin", adminHash)

	tokens, user, err := uc.Login(context.Background(), "admin", "admin-pass")
	if err != nil {
		t.Fatalf("Login() as admin unexpected error: %v", err)
	}
	if tokens.AccessToken == "" {
		t.Error("Login() as admin returned an empty token")
	}
	if user.Username != "admin" {
		t.Errorf("Login() as admin Username = %q, want %q", user.Username, "admin")
	}

	if _, _, err := uc.Login(context.Background(), "admin", "wrong-pass"); !errors.Is(err, apperr.ErrInvalidCredentials) {
		t.Errorf("Login() as admin with the wrong password error = %v, want apperr.ErrInvalidCredentials", err)
	}
}

func TestAuthUseCase_Login_AdminBootstrapDisabledWhenUnconfigured(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest(t, "", "")

	_, _, err := uc.Login(context.Background(), "admin", "admin-pass")
	if !errors.Is(err, apperr.ErrInvalidCredentials) {
		t.Errorf("Login() for 'admin' with no admin configured error = %v, want apperr.ErrInvalidCredentials", err)
	}
}

func TestAuthUseCase_Register_EnforcesConfiguredLengthPolicy(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest(t, "", "")
	cfg := AuthConfig{MinUsernameLength: 3, MaxUsernameLength: 50, MinPasswordLength: 8}

	tests := []struct {
		name     string
		username string
		password string
	}{
		{"username below the configured minimum", strings.Repeat("a", cfg.MinUsernameLength-1), "s3cret-pass"},
		{"username above the configured maximum", strings.Repeat("a", cfg.MaxUsernameLength+1), "s3cret-pass"},
		{"password below the configured minimum", "alice", strings.Repeat("p", cfg.MinPasswordLength-1)},
		{"password above bcrypt's 72-byte limit", "alice", strings.Repeat("p", password.MaxLength+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := uc.Register(context.Background(), tt.username, "user@example.com", tt.password)
			if !errors.Is(err, apperr.ErrValidation) {
				t.Errorf("Register() error = %v, want apperr.ErrValidation", err)
			}
		})
	}
}

func TestAuthUseCase_Register_RejectsReservedAdminName(t *testing.T) {
	adminHash, _ := testHasher(t).Hash("admin-pass")

	for _, name := range []string{"admin", "Admin", "ADMIN", "  admin  "} {
		t.Run(name, func(t *testing.T) {
			uc, _, _ := newAuthUseCaseForTest(t, "admin", adminHash)

			_, err := uc.Register(context.Background(), name, "someone@example.com", "user-pass-123")
			if !errors.Is(err, apperr.ErrConflict) {
				t.Fatalf("Register(%q) error = %v, want apperr.ErrConflict", name, err)
			}
		})
	}
}

// TestAuthUseCase_Register_NameNotReservedWithoutBootstrapAdmin: with no
// bootstrap admin configured the name is an ordinary one.
func TestAuthUseCase_Register_NameNotReservedWithoutBootstrapAdmin(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest(t, "", "")

	user, err := uc.Register(context.Background(), "admin", "admin@example.com", "user-pass-123")
	if err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}

	if _, _, err := uc.Login(context.Background(), "admin", "user-pass-123"); err != nil {
		t.Errorf("Login() for the registered user error = %v, want success", err)
	}
	if user.Username != "admin" {
		t.Errorf("Username = %q, want %q", user.Username, "admin")
	}
}

func TestAuthUseCase_Login_BootstrapAdminNameIsMatchedConsistently(t *testing.T) {
	adminHash, _ := testHasher(t).Hash("admin-pass")
	uc, _, _ := newAuthUseCaseForTest(t, "admin", adminHash)

	for _, name := range []string{"admin", "Admin", " ADMIN "} {
		_, user, err := uc.Login(context.Background(), name, "admin-pass")
		if err != nil {
			t.Errorf("Login(%q) error = %v, want success", name, err)
			continue
		}
		if user.Username != "admin" {
			t.Errorf("Login(%q) Username = %q, want %q", name, user.Username, "admin")
		}
	}
}

func TestAuthUseCase_Register_CountsCharactersNotBytes(t *testing.T) {
	cfg := AuthConfig{MinUsernameLength: 3, MaxUsernameLength: 50, MinPasswordLength: 8}

	tests := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{"cyrillic at the limit", strings.Repeat("и", cfg.MaxUsernameLength), false},
		{"cyrillic one over", strings.Repeat("и", cfg.MaxUsernameLength+1), true},
		{"emoji at the limit", strings.Repeat("🚀", cfg.MaxUsernameLength), false},
		{"emoji one over", strings.Repeat("🚀", cfg.MaxUsernameLength+1), true},
		{"cyrillic at the minimum", strings.Repeat("и", cfg.MinUsernameLength), false},
		{"cyrillic one under", strings.Repeat("и", cfg.MinUsernameLength-1), true},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, _, _ := newAuthUseCaseForTest(t, "", "")

			_, err := uc.Register(context.Background(), tt.username, fmt.Sprintf("u%d@example.com", i), "пароль-из-букв")
			if tt.wantErr {
				if !errors.Is(err, apperr.ErrValidation) {
					t.Errorf("Register(%d chars) error = %v, want apperr.ErrValidation", utf8.RuneCountInString(tt.username), err)
				}
				return
			}
			if err != nil {
				t.Errorf("Register(%d chars) unexpected error: %v", utf8.RuneCountInString(tt.username), err)
			}
		})
	}
}

// TestAuthUseCase_Register_PasswordMinimumIsCharactersCapIsBytes: the minimum
// is a policy stated in characters, the maximum is bcrypt's 72-byte limit.
func TestAuthUseCase_Register_PasswordMinimumIsCharactersCapIsBytes(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest(t, "", "")

	// 8 Cyrillic characters = 16 bytes: satisfies a minimum of 8 characters.
	if _, err := uc.Register(context.Background(), "alice", "a@example.com", strings.Repeat("п", 8)); err != nil {
		t.Errorf("an 8-character Cyrillic password should be accepted, got: %v", err)
	}

	// 40 Cyrillic characters = 80 bytes: past bcrypt's hard limit.
	uc2, _, _ := newAuthUseCaseForTest(t, "", "")
	_, err := uc2.Register(context.Background(), "bob", "b@example.com", strings.Repeat("п", 40))
	if !errors.Is(err, apperr.ErrValidation) {
		t.Errorf("a password over bcrypt's 72-byte limit should be rejected, got: %v", err)
	}
}

func TestAuthUseCase_Login_IssuesRefreshToken(t *testing.T) {
	uc, repo, _, refresh := newAuthUseCaseWithRefresh(t, "", "")
	hash, _ := testHasher(t).Hash("s3cret-pass")
	repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	tokens, _, err := uc.Login(context.Background(), "alice", "s3cret-pass")
	if err != nil {
		t.Fatalf("Login() unexpected error: %v", err)
	}
	if tokens.RefreshToken == "" {
		t.Fatal("Login() returned no refresh token")
	}
	if !tokens.RefreshExpiresAt.After(tokens.AccessExpiresAt) {
		t.Error("the refresh token should outlive the access token")
	}

	stored, err := refresh.GetByHash(context.Background(), (&fakeIssuer{}).HashRefreshToken(tokens.RefreshToken))
	if err != nil {
		t.Fatalf("the refresh token was not stored: %v", err)
	}
	if stored.TokenHash == tokens.RefreshToken {
		t.Error("the plaintext refresh token must not be stored, only its hash")
	}
}

func TestAuthUseCase_Refresh_RotatesAndRevokesTheOldToken(t *testing.T) {
	uc, repo, _, refresh := newAuthUseCaseWithRefresh(t, "", "")
	hash, _ := testHasher(t).Hash("s3cret-pass")
	repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	first, _, err := uc.Login(context.Background(), "alice", "s3cret-pass")
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}

	second, err := uc.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh(): %v", err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Error("Refresh() must hand out a new refresh token, not the same one")
	}

	old, _ := refresh.GetByHash(context.Background(), (&fakeIssuer{}).HashRefreshToken(first.RefreshToken))
	if old.RevokedAt == nil {
		t.Error("the rotated-out token should be revoked")
	}

	if _, err := uc.Refresh(context.Background(), second.RefreshToken); err != nil {
		t.Errorf("the newest refresh token should still work: %v", err)
	}
}

func TestAuthUseCase_Refresh_ReuseOfRevokedTokenKillsEverySession(t *testing.T) {
	uc, repo, _, refresh := newAuthUseCaseWithRefresh(t, "", "")
	hash, _ := testHasher(t).Hash("s3cret-pass")
	user, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	stolen, _, _ := uc.Login(context.Background(), "alice", "s3cret-pass")
	fresh, err := uc.Refresh(context.Background(), stolen.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh(): %v", err)
	}

	// The attacker replays the token the legitimate client already rotated.
	if _, err := uc.Refresh(context.Background(), stolen.RefreshToken); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("replaying a revoked token error = %v, want apperr.ErrUnauthorized", err)
	}

	// The session that replaced it must be taken down as well.
	if _, err := uc.Refresh(context.Background(), fresh.RefreshToken); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("sessions should be revoked after a replay, got: %v", err)
	}

	stored, _ := refresh.GetByHash(context.Background(), (&fakeIssuer{}).HashRefreshToken(fresh.RefreshToken))
	if stored.UserID != user.ID || stored.RevokedAt == nil {
		t.Error("every session of the user should be revoked")
	}
}

func TestAuthUseCase_Logout_RevokesTheSession(t *testing.T) {
	uc, repo, _, _ := newAuthUseCaseWithRefresh(t, "", "")
	hash, _ := testHasher(t).Hash("s3cret-pass")
	repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	tokens, _, _ := uc.Login(context.Background(), "alice", "s3cret-pass")

	if err := uc.Logout(context.Background(), tokens.RefreshToken); err != nil {
		t.Fatalf("Logout(): %v", err)
	}
	if _, err := uc.Refresh(context.Background(), tokens.RefreshToken); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("refreshing after logout error = %v, want apperr.ErrUnauthorized", err)
	}
}

func TestAuthUseCase_Logout_UnknownTokenIsNotAnError(t *testing.T) {
	uc, _, _, _ := newAuthUseCaseWithRefresh(t, "", "")

	if err := uc.Logout(context.Background(), "never-issued"); err != nil {
		t.Errorf("logging out an unknown session should succeed, got: %v", err)
	}
}

func TestAuthUseCase_Refresh_ExpiredTokenRejected(t *testing.T) {
	uc, repo, _, refresh := newAuthUseCaseWithRefresh(t, "", "")
	hash, _ := testHasher(t).Hash("s3cret-pass")
	user, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	issuer := &fakeIssuer{}
	plain, tokenHash, _ := issuer.NewRefreshToken()
	refresh.Create(context.Background(), domain.RefreshToken{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(-time.Minute),
	})

	if _, err := uc.Refresh(context.Background(), plain); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("an expired refresh token error = %v, want apperr.ErrUnauthorized", err)
	}
}

func TestAuthUseCase_Login_BootstrapAdminGetsNoRefreshToken(t *testing.T) {
	adminHash, _ := testHasher(t).Hash("admin-pass")
	uc, _, _, _ := newAuthUseCaseWithRefresh(t, "admin", adminHash)

	tokens, user, err := uc.Login(context.Background(), "admin", "admin-pass")
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}
	if tokens.AccessToken == "" {
		t.Error("the admin should still get an access token")
	}
	if tokens.RefreshToken != "" {
		t.Error("the bootstrap admin has no row in users, so it must not get a refresh token")
	}
	if user.Username != "admin" {
		t.Errorf("Username = %q, want admin", user.Username)
	}
}

func TestAuthUseCase_Login_IsCaseInsensitiveOnUsername(t *testing.T) {
	uc, repo, _, _ := newAuthUseCaseWithRefresh(t, "", "")
	hash, _ := testHasher(t).Hash("s3cret-pass")
	repo.Create(context.Background(), domain.User{Username: "Alice", Email: "a@example.com", PasswordHash: hash})

	for _, name := range []string{"alice", "ALICE", " Alice "} {
		if _, _, err := uc.Login(context.Background(), name, "s3cret-pass"); err != nil {
			t.Errorf("Login(%q) error = %v, want success", name, err)
		}
	}
}

func TestAuthUseCase_Register_RejectsNameTakenInAnotherCase(t *testing.T) {
	uc, _, _, _ := newAuthUseCaseWithRefresh(t, "", "")

	if _, err := uc.Register(context.Background(), "Alice", "a@example.com", "s3cret-pass"); err != nil {
		t.Fatalf("first Register(): %v", err)
	}

	_, err := uc.Register(context.Background(), "alice", "other@example.com", "s3cret-pass")
	if !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("registering a name that differs only by case error = %v, want apperr.ErrConflict", err)
	}
}

func TestAuthUseCase_Refresh_RollsBackWhenTheReplacementCannotBeStored(t *testing.T) {
	uc, repo, _, refresh := newAuthUseCaseWithRefresh(t, "", "")
	hash, _ := testHasher(t).Hash("s3cret-pass")
	repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	tokens, _, err := uc.Login(context.Background(), "alice", "s3cret-pass")
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}

	refresh.failInsert = true
	if _, err := uc.Refresh(context.Background(), tokens.RefreshToken); err == nil {
		t.Fatal("Refresh() should fail when the replacement cannot be stored")
	}

	refresh.failInsert = false
	if _, err := uc.Refresh(context.Background(), tokens.RefreshToken); err != nil {
		t.Errorf("the presented token must survive a failed rotation, got: %v", err)
	}
}

func TestAuthUseCase_Refresh_OnlyOneConcurrentCallerWins(t *testing.T) {
	uc, repo, _, _ := newAuthUseCaseWithRefresh(t, "", "")
	hash, _ := testHasher(t).Hash("s3cret-pass")
	repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	tokens, _, err := uc.Login(context.Background(), "alice", "s3cret-pass")
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}

	const callers = 8
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(callers)
	results := make([]error, callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer done.Done()
			start.Wait()
			_, results[i] = uc.Refresh(context.Background(), tokens.RefreshToken)
		}(i)
	}
	start.Done()
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
		t.Errorf("%d callers exchanged the same token, want exactly 1", won)
	}
}
