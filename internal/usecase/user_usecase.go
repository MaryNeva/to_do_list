package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
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
	DefaultPageSize   int
	MaxPageSize       int
	AdminUsername     string
}

type UserUseCase struct {
	repo    UserRepository
	hasher  *password.Hasher
	cfg     UserConfig
	logger  *slog.Logger
	metrics MetricsRecorder
}

func NewUserUseCase(repo UserRepository, hasher *password.Hasher, cfg UserConfig, logger *slog.Logger, opts ...Option) *UserUseCase {
	resolved := applyOptions(opts)
	return &UserUseCase{repo: repo, hasher: hasher, cfg: cfg, logger: logger, metrics: resolved.metrics}
}

func (uc *UserUseCase) Get(ctx context.Context, actor domain.Claims, id int64) (domain.User, error) {
	if err := authorizeUser(actor, id, false); err != nil {
		return domain.User{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()
	return uc.repo.GetByID(ctx, id)
}

func (uc *UserUseCase) List(ctx context.Context, actor domain.Claims, page domain.PageRequest) (domain.Page[domain.User], error) {
	if err := authorizeUser(actor, 0, true); err != nil {
		return domain.Page[domain.User]{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()

	page = clampPage(page, uc.cfg.DefaultPageSize, uc.cfg.MaxPageSize)

	users, err := uc.repo.List(ctx, page)
	if err != nil {
		uc.logger.ErrorContext(ctx, "list users failed", "error", err)
		return domain.Page[domain.User]{}, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

func (uc *UserUseCase) Update(ctx context.Context, actor domain.Claims, id int64, edit domain.UserEdit) (domain.User, error) {
	if err := authorizeUser(actor, id, false); err != nil {
		return domain.User{}, err
	}
	if edit.IsEmpty() {
		return domain.User{}, fmt.Errorf("%w: at least one of username, email, password must be present", apperr.ErrValidation)
	}

	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()

	var fields domain.UserUpdate

	if edit.Username != nil {
		username := normalizeUsername(*edit.Username)
		if username == "" {
			return domain.User{}, fmt.Errorf("%w: username must not be empty", apperr.ErrValidation)
		}
		if err := validateUsername(username, uc.cfg.MinUsernameLength, uc.cfg.MaxUsernameLength); err != nil {
			return domain.User{}, err
		}
		if isReservedUsername(uc.cfg.AdminUsername, username) {
			return domain.User{}, fmt.Errorf("%w: username %q is reserved", apperr.ErrConflict, username)
		}
		fields.Username = &username
	}
	if edit.Email != nil {
		// An empty email is rejected: the address cannot be removed.
		email := strings.TrimSpace(*edit.Email)
		if err := validateEmail(email); err != nil {
			return domain.User{}, err
		}
		fields.Email = &email
	}
	if edit.Password != nil {
		// Passwords are not trimmed; surrounding spaces are significant.
		newPassword := *edit.Password
		if err := validatePassword(newPassword, uc.cfg.MinPasswordLength); err != nil {
			return domain.User{}, err
		}
		hash, err := uc.hasher.Hash(ctx, newPassword)
		if err != nil {
			return domain.User{}, fmt.Errorf("hash password: %w", err)
		}
		fields.PasswordHash = &hash
	}

	if fields.PasswordHash == nil {
		return uc.update(ctx, id, fields)
	}

	updated, revoked, err := uc.repo.UpdateAndRevokeSessions(ctx, id, fields)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) || errors.Is(err, apperr.ErrConflict) {
			return domain.User{}, err
		}
		uc.logger.ErrorContext(ctx, "update user failed", "error", err, "user_id", id)
		return domain.User{}, fmt.Errorf("update user: %w", err)
	}

	uc.metrics.SessionsRevoked(ReasonPasswordChange, revoked)

	return updated, nil
}

func (uc *UserUseCase) update(ctx context.Context, id int64, fields domain.UserUpdate) (domain.User, error) {
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

func (uc *UserUseCase) Delete(ctx context.Context, actor domain.Claims, id int64) error {
	if err := authorizeUser(actor, id, false); err != nil {
		return err
	}
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

// maxEmailLength is the RFC 5321 limit: 64 (local part) + 1 + 255 (domain).
const maxEmailLength = 320

// validateEmail repeats the DTO check so callers that bypass HTTP are validated too.
func validateEmail(email string) error {
	if email == "" {
		return fmt.Errorf("%w: email is required", apperr.ErrValidation)
	}
	if utf8.RuneCountInString(email) > maxEmailLength {
		return fmt.Errorf("%w: email must be at most %d characters", apperr.ErrValidation, maxEmailLength)
	}

	// ParseAddress accepts display names ("Mary <m@e.com>"); require a bare address.
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return fmt.Errorf("%w: %q is not a valid email address", apperr.ErrValidation, email)
	}
	return nil
}

func validatePassword(plain string, min int) error {
	switch {
	// Checked separately so a configured minimum of 0 cannot allow an empty password.
	case plain == "":
		return fmt.Errorf("%w: password must not be empty", apperr.ErrValidation)
	case utf8.RuneCountInString(plain) < min:
		return fmt.Errorf("%w: password must be at least %d characters", apperr.ErrValidation, min)
	case len(plain) > password.MaxLength:
		return fmt.Errorf("%w: password must be at most %d bytes", apperr.ErrValidation, password.MaxLength)
	default:
		return nil
	}
}

func clampPage(page domain.PageRequest, defaultSize, maxSize int) domain.PageRequest {
	if page.Limit <= 0 {
		page.Limit = defaultSize
	}
	if page.Limit > maxSize {
		page.Limit = maxSize
	}
	if page.Offset < 0 {
		page.Offset = 0
	}
	return page
}

// authorizeUser allows admins, and users acting on their own id unless
// adminOnly is set. actor must come from verified token claims.
func authorizeUser(actor domain.Claims, targetID int64, adminOnly bool) error {
	if actor.UserID < 0 || (actor.UserID == 0 && !actor.IsAdmin) {
		return apperr.ErrUnauthorized
	}
	if actor.IsAdmin {
		return nil
	}
	if !adminOnly && targetID > 0 && actor.UserID == targetID {
		return nil
	}
	return apperr.ErrForbidden
}
