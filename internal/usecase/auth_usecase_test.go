package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	repo := newFakeUserRepo()
	tokens := &fakeTokenService{}
	cfg := AuthConfig{
		AdminUsername:     adminUsername,
		AdminPasswordHash: adminPasswordHash,
		Timeout:           time.Second,
		MinUsernameLength: 3,
		MaxUsernameLength: 50,
		MinPasswordLength: 8,
	}
	uc := NewAuthUseCase(repo, tokens, testHasher(t), cfg, silentLogger())
	return uc, repo, tokens
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

	tokenString, expiresAt, user, err := uc.Login(context.Background(), "alice", "s3cret-pass")
	if err != nil {
		t.Fatalf("Login() unexpected error: %v", err)
	}
	if tokenString == "" {
		t.Error("Login() returned an empty token")
	}
	if !expiresAt.After(time.Now()) {
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

	_, _, _, err := uc.Login(context.Background(), "alice", "wrong-password")
	if !errors.Is(err, apperr.ErrInvalidCredentials) {
		t.Errorf("Login() with the wrong password error = %v, want apperr.ErrInvalidCredentials", err)
	}
}

func TestAuthUseCase_Login_UnknownUser_SameErrorAsWrongPassword(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest(t, "", "")

	_, _, _, err := uc.Login(context.Background(), "ghost", "whatever")
	if !errors.Is(err, apperr.ErrInvalidCredentials) {
		t.Errorf("Login() for an unknown user error = %v, want apperr.ErrInvalidCredentials", err)
	}
}

func TestAuthUseCase_Login_AdminBootstrap(t *testing.T) {
	adminHash, _ := testHasher(t).Hash("admin-pass")
	uc, _, _ := newAuthUseCaseForTest(t, "admin", adminHash)

	tokenString, _, user, err := uc.Login(context.Background(), "admin", "admin-pass")
	if err != nil {
		t.Fatalf("Login() as admin unexpected error: %v", err)
	}
	if tokenString == "" {
		t.Error("Login() as admin returned an empty token")
	}
	if user.Username != "admin" {
		t.Errorf("Login() as admin Username = %q, want %q", user.Username, "admin")
	}

	if _, _, _, err := uc.Login(context.Background(), "admin", "wrong-pass"); !errors.Is(err, apperr.ErrInvalidCredentials) {
		t.Errorf("Login() as admin with the wrong password error = %v, want apperr.ErrInvalidCredentials", err)
	}
}

func TestAuthUseCase_Login_AdminBootstrapDisabledWhenUnconfigured(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest(t, "", "")

	_, _, _, err := uc.Login(context.Background(), "admin", "admin-pass")
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

// TestAuthUseCase_Register_RejectsReservedAdminName covers the name clash:
// Login always routes the configured admin name to the bootstrap hash, so an
// account registered under it could never sign in with its own password.
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

	if _, _, _, err := uc.Login(context.Background(), "admin", "user-pass-123"); err != nil {
		t.Errorf("Login() for the registered user error = %v, want success", err)
	}
	if user.Username != "admin" {
		t.Errorf("Username = %q, want %q", user.Username, "admin")
	}
}

// TestAuthUseCase_Login_BootstrapAdminNameIsMatchedConsistently: the same
// normalization decides both that a name is reserved and that a login goes
// to the bootstrap path, so no case variant can fall between the two.
func TestAuthUseCase_Login_BootstrapAdminNameIsMatchedConsistently(t *testing.T) {
	adminHash, _ := testHasher(t).Hash("admin-pass")
	uc, _, _ := newAuthUseCaseForTest(t, "admin", adminHash)

	for _, name := range []string{"admin", "Admin", " ADMIN "} {
		_, _, user, err := uc.Login(context.Background(), name, "admin-pass")
		if err != nil {
			t.Errorf("Login(%q) error = %v, want success", name, err)
			continue
		}
		if user.Username != "admin" {
			t.Errorf("Login(%q) Username = %q, want %q", name, user.Username, "admin")
		}
	}
}

// TestAuthUseCase_Register_CountsCharactersNotBytes: a Cyrillic or emoji name
// at the configured limit must be accepted, and one character past it
// rejected - the limits are stated in characters.
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
