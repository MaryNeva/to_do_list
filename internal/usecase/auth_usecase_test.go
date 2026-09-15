package usecase

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"to-do-list/internal/apperr"
	"to-do-list/internal/auth/password"
	"to-do-list/internal/domain"
)

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

func newAuthUseCaseForTest(adminUsername, adminPasswordHash string) (*AuthUseCase, *fakeUserRepo, *fakeTokenService) {
	repo := newFakeUserRepo()
	tokens := &fakeTokenService{}
	uc := NewAuthUseCase(repo, tokens, adminUsername, adminPasswordHash, time.Second, silentLogger())
	return uc, repo, tokens
}

func TestAuthUseCase_Register(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest("", "")

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
	uc, _, _ := newAuthUseCaseForTest("", "")

	if _, err := uc.Register(context.Background(), "alice", "alice@example.com", "s3cret-pass"); err != nil {
		t.Fatalf("first Register() unexpected error: %v", err)
	}

	_, err := uc.Register(context.Background(), "alice", "different@example.com", "another-pass")
	if !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("Register() with a duplicate username error = %v, want apperr.ErrConflict", err)
	}
}

func TestAuthUseCase_Register_MissingFields(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest("", "")

	if _, err := uc.Register(context.Background(), "", "a@example.com", "pass1234"); !errors.Is(err, apperr.ErrValidation) {
		t.Errorf("Register() with empty username error = %v, want apperr.ErrValidation", err)
	}
}

func TestAuthUseCase_Login_Success(t *testing.T) {
	uc, repo, _ := newAuthUseCaseForTest("", "")

	hash, _ := password.Hash("s3cret-pass")
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
	uc, repo, _ := newAuthUseCaseForTest("", "")
	hash, _ := password.Hash("s3cret-pass")
	repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	_, _, _, err := uc.Login(context.Background(), "alice", "wrong-password")
	if !errors.Is(err, apperr.ErrInvalidCredentials) {
		t.Errorf("Login() with the wrong password error = %v, want apperr.ErrInvalidCredentials", err)
	}
}

func TestAuthUseCase_Login_UnknownUser_SameErrorAsWrongPassword(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest("", "")

	_, _, _, err := uc.Login(context.Background(), "ghost", "whatever")
	if !errors.Is(err, apperr.ErrInvalidCredentials) {
		t.Errorf("Login() for an unknown user error = %v, want apperr.ErrInvalidCredentials", err)
	}
}

func TestAuthUseCase_Login_AdminBootstrap(t *testing.T) {
	adminHash, _ := password.Hash("admin-pass")
	uc, _, _ := newAuthUseCaseForTest("admin", adminHash)

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
	uc, _, _ := newAuthUseCaseForTest("", "")

	_, _, _, err := uc.Login(context.Background(), "admin", "admin-pass")
	if !errors.Is(err, apperr.ErrInvalidCredentials) {
		t.Errorf("Login() for 'admin' with no admin configured error = %v, want apperr.ErrInvalidCredentials", err)
	}
}
