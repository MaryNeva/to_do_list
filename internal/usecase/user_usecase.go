package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"to-do-list/internal/apperr"
	"to-do-list/internal/auth/password"
	"to-do-list/internal/domain"
)

type UserConfig struct {
	Timeout           time.Duration
	MinUsernameLength int
	MaxUsernameLength int
	MinPasswordLength int
	AdminUsername     string
}

type UserUseCase struct {
	repo   domain.UserRepository
	hasher *password.Hasher
	cfg    UserConfig
	logger *slog.Logger
}

func NewUserUseCase(repo domain.UserRepository, hasher *password.Hasher, cfg UserConfig, logger *slog.Logger) *UserUseCase {
	return &UserUseCase{repo: repo, hasher: hasher, cfg: cfg, logger: logger}
}

var _ domain.UserService = (*UserUseCase)(nil)

func (uc *UserUseCase) Get(ctx context.Context, id int64) (domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()
	return uc.repo.GetByID(ctx, id)
}

func (uc *UserUseCase) List(ctx context.Context) ([]domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()
	users, err := uc.repo.List(ctx)
	if err != nil {
		uc.logger.ErrorContext(ctx, "list users failed", "error", err)
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

func (uc *UserUseCase) Update(ctx context.Context, id int64, username, email, newPassword string) (domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()

	var fields domain.UserUpdate

	if username = normalizeUsername(username); username != "" {
		if err := validateUsername(username, uc.cfg.MinUsernameLength, uc.cfg.MaxUsernameLength); err != nil {
			return domain.User{}, err
		}
		if isReservedUsername(uc.cfg.AdminUsername, username) {
			return domain.User{}, fmt.Errorf("%w: username %q is reserved", apperr.ErrConflict, username)
		}
		fields.Username = &username
	}
	if email = strings.TrimSpace(email); email != "" {
		fields.Email = &email
	}
	if newPassword != "" {
		if err := validatePassword(newPassword, uc.cfg.MinPasswordLength); err != nil {
			return domain.User{}, err
		}
		hash, err := uc.hasher.Hash(newPassword)
		if err != nil {
			return domain.User{}, fmt.Errorf("hash password: %w", err)
		}
		fields.PasswordHash = &hash
	}

	updated, err := uc.repo.Update(ctx, id, fields)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) || errors.Is(err, apperr.ErrConflict) {
			return domain.User{}, err
		}
		uc.logger.ErrorContext(ctx, "update user failed", "error", err, "user_id", id)
		return domain.User{}, fmt.Errorf("update user: %w", err)
	}

	return updated, nil
}

func (uc *UserUseCase) Delete(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()
	if err := uc.repo.Delete(ctx, id); err != nil {
		uc.logger.ErrorContext(ctx, "delete user failed", "error", err, "user_id", id)
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

func normalizeUsername(username string) string {
	return strings.TrimSpace(username)
}

func isReservedUsername(adminUsername, name string) bool {
	adminUsername = normalizeUsername(adminUsername)
	if adminUsername == "" {
		return false
	}
	return strings.EqualFold(normalizeUsername(name), adminUsername)
}

func validateUsername(username string, min, max int) error {
	switch count := utf8.RuneCountInString(username); {
	case count < min:
		return fmt.Errorf("%w: username must be at least %d characters", apperr.ErrValidation, min)
	case count > max:
		return fmt.Errorf("%w: username must be at most %d characters", apperr.ErrValidation, max)
	default:
		return nil
	}
}

func validatePassword(plain string, min int) error {
	switch {
	case utf8.RuneCountInString(plain) < min:
		return fmt.Errorf("%w: password must be at least %d characters", apperr.ErrValidation, min)
	case len(plain) > password.MaxLength:
		return fmt.Errorf("%w: password must be at most %d bytes", apperr.ErrValidation, password.MaxLength)
	default:
		return nil
	}
}
