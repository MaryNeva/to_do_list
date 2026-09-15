package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"to-do-list/internal/auth/password"
	"to-do-list/internal/domain"
)

type UserUseCase struct {
	repo    domain.UserRepository
	timeout time.Duration
	logger  *slog.Logger
}

func NewUserUseCase(repo domain.UserRepository, timeout time.Duration, logger *slog.Logger) *UserUseCase {
	return &UserUseCase{repo: repo, timeout: timeout, logger: logger}
}

var _ domain.UserService = (*UserUseCase)(nil)

func (uc *UserUseCase) Get(ctx context.Context, id int64) (domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.timeout)
	defer cancel()
	return uc.repo.GetByID(ctx, id)
}

func (uc *UserUseCase) List(ctx context.Context) ([]domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.timeout)
	defer cancel()
	users, err := uc.repo.List(ctx)
	if err != nil {
		uc.logger.ErrorContext(ctx, "list users failed", "error", err)
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

func (uc *UserUseCase) Update(ctx context.Context, id int64, username, email, newPassword string) (domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.timeout)
	defer cancel()

	existing, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return domain.User{}, err
	}

	if username = strings.TrimSpace(username); username != "" {
		existing.Username = username
	}
	if email = strings.TrimSpace(email); email != "" {
		existing.Email = email
	}
	if newPassword != "" {
		hash, err := password.Hash(newPassword)
		if err != nil {
			return domain.User{}, fmt.Errorf("hash password: %w", err)
		}
		existing.PasswordHash = hash
	}

	if err := uc.repo.Update(ctx, existing); err != nil {
		uc.logger.ErrorContext(ctx, "update user failed", "error", err, "user_id", id)
		return domain.User{}, fmt.Errorf("update user: %w", err)
	}

	return uc.repo.GetByID(ctx, id)
}

func (uc *UserUseCase) Delete(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, uc.timeout)
	defer cancel()
	if err := uc.repo.Delete(ctx, id); err != nil {
		uc.logger.ErrorContext(ctx, "delete user failed", "error", err, "user_id", id)
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}
