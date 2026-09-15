package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"to-do-list/internal/apperr"
	"to-do-list/internal/auth/password"
	"to-do-list/internal/domain"
)

type TokenService interface {
	Generate(userID int64, username string, isAdmin bool) (tokenString string, expiresAt time.Time, err error)
	Parse(tokenString string) (domain.Claims, error)
}

type AuthUseCase struct {
	users             domain.UserRepository
	tokens            TokenService
	adminUsername     string
	adminPasswordHash string
	timeout           time.Duration
	logger            *slog.Logger
}

func NewAuthUseCase(
	users domain.UserRepository,
	tokens TokenService,
	adminUsername, adminPasswordHash string,
	timeout time.Duration,
	logger *slog.Logger,
) *AuthUseCase {
	return &AuthUseCase{
		users:             users,
		tokens:            tokens,
		adminUsername:     adminUsername,
		adminPasswordHash: adminPasswordHash,
		timeout:           timeout,
		logger:            logger,
	}
}

var _ domain.AuthService = (*AuthUseCase)(nil)

func (a *AuthUseCase) Register(ctx context.Context, username, email, plainPassword string) (domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	username = strings.TrimSpace(username)
	email = strings.TrimSpace(email)

	if username == "" || email == "" || plainPassword == "" {
		return domain.User{}, fmt.Errorf("%w: username, email and password are required", apperr.ErrValidation)
	}

	hash, err := password.Hash(plainPassword)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}

	created, err := a.users.Create(ctx, domain.User{
		Username:     username,
		Email:        email,
		PasswordHash: hash,
	})
	if err != nil {
		if errors.Is(err, apperr.ErrConflict) {
			return domain.User{}, apperr.ErrConflict
		}
		a.logger.ErrorContext(ctx, "register user failed", "error", err, "username", username)
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}

	return created, nil
}

func (a *AuthUseCase) Login(ctx context.Context, username, plainPassword string) (string, time.Time, domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	username = strings.TrimSpace(username)

	if a.adminUsername != "" && username == a.adminUsername {
		if err := password.Verify(a.adminPasswordHash, plainPassword); err != nil {
			return "", time.Time{}, domain.User{}, apperr.ErrInvalidCredentials
		}
		admin := domain.User{ID: 0, Username: a.adminUsername}
		tokenString, expiresAt, err := a.tokens.Generate(admin.ID, admin.Username, true)
		if err != nil {
			return "", time.Time{}, domain.User{}, fmt.Errorf("generate token: %w", err)
		}
		return tokenString, expiresAt, admin, nil
	}

	user, err := a.users.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Same error as a wrong password: do not reveal whether the
			// username exists.
			return "", time.Time{}, domain.User{}, apperr.ErrInvalidCredentials
		}
		return "", time.Time{}, domain.User{}, fmt.Errorf("get user: %w", err)
	}

	if !password.Matches(user.PasswordHash, plainPassword) {
		return "", time.Time{}, domain.User{}, apperr.ErrInvalidCredentials
	}

	tokenString, expiresAt, err := a.tokens.Generate(user.ID, user.Username, false)
	if err != nil {
		return "", time.Time{}, domain.User{}, fmt.Errorf("generate token: %w", err)
	}

	return tokenString, expiresAt, user, nil
}

func (a *AuthUseCase) ValidateToken(_ context.Context, tokenString string) (domain.Claims, error) {
	return a.tokens.Parse(tokenString)
}
