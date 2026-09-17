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

type RefreshTokenIssuer interface {
	NewRefreshToken() (plain, hash string, err error)
	HashRefreshToken(plain string) string
}

type AuthUseCase struct {
	users   domain.UserRepository
	tokens  TokenService
	refresh domain.RefreshTokenRepository
	issuer  RefreshTokenIssuer
	hasher  *password.Hasher
	cfg     AuthConfig
	logger  *slog.Logger
}

type AuthConfig struct {
	AdminUsername     string
	AdminPasswordHash string
	Timeout           time.Duration
	MinUsernameLength int
	MaxUsernameLength int
	MinPasswordLength int
	RefreshTTL        time.Duration
}

func NewAuthUseCase(
	users domain.UserRepository,
	tokens TokenService,
	refresh domain.RefreshTokenRepository,
	issuer RefreshTokenIssuer,
	hasher *password.Hasher,
	cfg AuthConfig,
	logger *slog.Logger,
) *AuthUseCase {
	return &AuthUseCase{
		users:   users,
		tokens:  tokens,
		refresh: refresh,
		issuer:  issuer,
		hasher:  hasher,
		cfg:     cfg,
		logger:  logger,
	}
}

var _ domain.AuthService = (*AuthUseCase)(nil)

func (a *AuthUseCase) Register(ctx context.Context, username, email, plainPassword string) (domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	username = normalizeUsername(username)
	email = strings.TrimSpace(email)

	if username == "" || email == "" || plainPassword == "" {
		return domain.User{}, fmt.Errorf("%w: username, email and password are required", apperr.ErrValidation)
	}
	if err := validateUsername(username, a.cfg.MinUsernameLength, a.cfg.MaxUsernameLength); err != nil {
		return domain.User{}, err
	}

	if isReservedUsername(a.cfg.AdminUsername, username) {
		return domain.User{}, fmt.Errorf("%w: username %q is reserved", apperr.ErrConflict, username)
	}
	if err := validatePassword(plainPassword, a.cfg.MinPasswordLength); err != nil {
		return domain.User{}, err
	}

	hash, err := a.hasher.Hash(plainPassword)
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

func (a *AuthUseCase) Login(ctx context.Context, username, plainPassword string) (domain.Tokens, domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	username = normalizeUsername(username)

	if isReservedUsername(a.cfg.AdminUsername, username) {
		if err := password.Verify(a.cfg.AdminPasswordHash, plainPassword); err != nil {
			return domain.Tokens{}, domain.User{}, apperr.ErrInvalidCredentials
		}
		admin := domain.User{ID: 0, Username: normalizeUsername(a.cfg.AdminUsername)}
		access, expiresAt, err := a.tokens.Generate(admin.ID, admin.Username, true)
		if err != nil {
			return domain.Tokens{}, domain.User{}, fmt.Errorf("generate token: %w", err)
		}
		// The bootstrap admin has no row in users, so it cannot own a
		// refresh token; it re-authenticates with its configured password.
		return domain.Tokens{AccessToken: access, AccessExpiresAt: expiresAt}, admin, nil
	}

	user, err := a.users.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return domain.Tokens{}, domain.User{}, apperr.ErrInvalidCredentials
		}
		return domain.Tokens{}, domain.User{}, fmt.Errorf("get user: %w", err)
	}

	if !password.Matches(user.PasswordHash, plainPassword) {
		return domain.Tokens{}, domain.User{}, apperr.ErrInvalidCredentials
	}

	tokens, err := a.issueTokens(ctx, user)
	if err != nil {
		return domain.Tokens{}, domain.User{}, err
	}

	return tokens, user, nil
}

func (a *AuthUseCase) Refresh(ctx context.Context, refreshToken string) (domain.Tokens, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return domain.Tokens{}, fmt.Errorf("%w: refresh token is required", apperr.ErrUnauthorized)
	}

	plain, hash, err := a.issuer.NewRefreshToken()
	if err != nil {
		return domain.Tokens{}, fmt.Errorf("generate refresh token: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(a.cfg.RefreshTTL)

	result, err := a.refresh.Rotate(ctx, a.issuer.HashRefreshToken(refreshToken), domain.RefreshToken{
		TokenHash: hash,
		ExpiresAt: expiresAt,
	}, now)
	switch {
	case err == nil:
	case errors.Is(err, apperr.ErrConflict):
		if revokeErr := a.refresh.RevokeAllForUser(ctx, result.UserID); revokeErr != nil {
			a.logger.ErrorContext(ctx, "revoking sessions after refresh token reuse failed", "error", revokeErr, "user_id", result.UserID)
		}
		a.logger.WarnContext(ctx, "refresh token reused after it was consumed", "user_id", result.UserID)
		return domain.Tokens{}, fmt.Errorf("%w: refresh token is no longer valid", apperr.ErrUnauthorized)
	case errors.Is(err, apperr.ErrNotFound):
		return domain.Tokens{}, fmt.Errorf("%w: unknown refresh token", apperr.ErrUnauthorized)
	case errors.Is(err, apperr.ErrUnauthorized):
		return domain.Tokens{}, err
	default:
		return domain.Tokens{}, fmt.Errorf("rotate refresh token: %w", err)
	}

	user, err := a.users.GetByID(ctx, result.UserID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return domain.Tokens{}, fmt.Errorf("%w: account no longer exists", apperr.ErrUnauthorized)
		}
		return domain.Tokens{}, fmt.Errorf("get user: %w", err)
	}

	access, accessExpiresAt, err := a.tokens.Generate(user.ID, user.Username, false)
	if err != nil {
		return domain.Tokens{}, fmt.Errorf("generate token: %w", err)
	}

	return domain.Tokens{
		AccessToken:      access,
		AccessExpiresAt:  accessExpiresAt,
		RefreshToken:     plain,
		RefreshExpiresAt: expiresAt,
	}, nil
}

func (a *AuthUseCase) Logout(ctx context.Context, refreshToken string) error {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return fmt.Errorf("%w: refresh token is required", apperr.ErrUnauthorized)
	}

	err := a.refresh.Revoke(ctx, a.issuer.HashRefreshToken(refreshToken))
	switch {
	case err == nil, errors.Is(err, apperr.ErrNotFound):
		return nil
	default:
		return fmt.Errorf("revoke refresh token: %w", err)
	}
}

func (a *AuthUseCase) issueTokens(ctx context.Context, user domain.User) (domain.Tokens, error) {
	access, accessExpiresAt, err := a.tokens.Generate(user.ID, user.Username, false)
	if err != nil {
		return domain.Tokens{}, fmt.Errorf("generate token: %w", err)
	}

	plain, hash, err := a.issuer.NewRefreshToken()
	if err != nil {
		return domain.Tokens{}, fmt.Errorf("generate refresh token: %w", err)
	}

	refreshExpiresAt := time.Now().Add(a.cfg.RefreshTTL)
	if _, err := a.refresh.Create(ctx, domain.RefreshToken{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: refreshExpiresAt,
	}); err != nil {
		return domain.Tokens{}, fmt.Errorf("store refresh token: %w", err)
	}

	return domain.Tokens{
		AccessToken:      access,
		AccessExpiresAt:  accessExpiresAt,
		RefreshToken:     plain,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

func (a *AuthUseCase) ValidateToken(_ context.Context, tokenString string) (domain.Claims, error) {
	return a.tokens.Parse(tokenString)
}
