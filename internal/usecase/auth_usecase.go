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
	users   AuthUsers
	tokens  TokenService
	refresh SessionStore
	issuer  RefreshTokenIssuer
	hasher  *password.Hasher
	cfg     AuthConfig
	logger  *slog.Logger
	metrics MetricsRecorder
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
	users AuthUsers,
	tokens TokenService,
	refresh SessionStore,
	issuer RefreshTokenIssuer,
	hasher *password.Hasher,
	cfg AuthConfig,
	logger *slog.Logger,
	opts ...Option,
) *AuthUseCase {
	resolved := applyOptions(opts)

	return &AuthUseCase{
		users:   users,
		tokens:  tokens,
		refresh: refresh,
		issuer:  issuer,
		hasher:  hasher,
		cfg:     cfg,
		logger:  logger,
		metrics: resolved.metrics,
	}
}

func (a *AuthUseCase) Register(ctx context.Context, username, email, plainPassword string) (domain.User, error) {
	user, err := a.register(ctx, username, email, plainPassword)
	a.metrics.AuthAttempt(OperationRegister, outcomeOf(err))
	return user, err
}

func (a *AuthUseCase) register(ctx context.Context, username, email, plainPassword string) (domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	username = normalizeUsername(username)
	email = strings.TrimSpace(email)

	if username == "" || email == "" || plainPassword == "" {
		return domain.User{}, fmt.Errorf("%w: username, email and password are required", apperr.ErrValidation)
	}
	if err := validateEmail(email); err != nil {
		return domain.User{}, err
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

	hash, err := a.hasher.Hash(ctx, plainPassword)
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
	tokens, user, err := a.login(ctx, username, plainPassword)
	a.metrics.AuthAttempt(OperationLogin, outcomeOf(err))
	return tokens, user, err
}

func (a *AuthUseCase) login(ctx context.Context, username, plainPassword string) (domain.Tokens, domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	username = normalizeUsername(username)

	if isReservedUsername(a.cfg.AdminUsername, username) {
		if err := a.hasher.Verify(ctx, a.cfg.AdminPasswordHash, plainPassword); err != nil {
			if errors.Is(err, password.ErrMismatch) {
				return domain.Tokens{}, domain.User{}, apperr.ErrInvalidCredentials
			}
			return domain.Tokens{}, domain.User{}, fmt.Errorf("verify admin password: %w", err)
		}
		admin := domain.User{ID: 0, Username: normalizeUsername(a.cfg.AdminUsername)}
		access, expiresAt, err := a.tokens.Generate(admin.ID, admin.Username, true)
		if err != nil {
			return domain.Tokens{}, domain.User{}, fmt.Errorf("generate token: %w", err)
		}

		return domain.Tokens{AccessToken: access, AccessExpiresAt: expiresAt}, admin, nil
	}

	user, err := a.users.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return domain.Tokens{}, domain.User{}, apperr.ErrInvalidCredentials
		}
		return domain.Tokens{}, domain.User{}, fmt.Errorf("get user: %w", err)
	}

	// Anything other than a mismatch means the comparison never ran - most
	// likely the hashing queue is full - and must not read as a bad password.
	if err := a.hasher.Verify(ctx, user.PasswordHash, plainPassword); err != nil {
		if errors.Is(err, password.ErrMismatch) {
			return domain.Tokens{}, domain.User{}, apperr.ErrInvalidCredentials
		}
		return domain.Tokens{}, domain.User{}, fmt.Errorf("verify password: %w", err)
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

	expiresAt := time.Now().Add(a.cfg.RefreshTTL)

	result, err := a.refresh.Rotate(ctx, a.issuer.HashRefreshToken(refreshToken), domain.RefreshToken{
		TokenHash: hash,
		ExpiresAt: expiresAt,
	})
	switch {
	case err == nil:
		a.metrics.RefreshRotation(OutcomeSuccess)
	case errors.Is(err, apperr.ErrTokenReuse):
		a.metrics.RefreshRotation(OutcomeReuse)
		a.metrics.SessionsRevoked(ReasonTokenReuse, result.SessionsRevoked)
		a.logger.WarnContext(ctx, "refresh token reused after it was consumed",
			"user_id", result.UserID, "sessions_revoked", result.SessionsRevoked)
		return domain.Tokens{}, fmt.Errorf("%w: refresh token is no longer valid", apperr.ErrUnauthorized)
	case errors.Is(err, apperr.ErrConflict):
		a.metrics.RefreshRotation(OutcomeFailure)
		a.logger.ErrorContext(ctx, "generated refresh token collided with an existing one")
		return domain.Tokens{}, fmt.Errorf("rotate refresh token: %w", err)
	case errors.Is(err, apperr.ErrNotFound):
		a.metrics.RefreshRotation(OutcomeUnknown)
		return domain.Tokens{}, fmt.Errorf("%w: unknown refresh token", apperr.ErrUnauthorized)
	case errors.Is(err, apperr.ErrUnauthorized):
		a.metrics.RefreshRotation(OutcomeExpired)
		return domain.Tokens{}, err
	default:
		a.metrics.RefreshRotation(OutcomeFailure)
		return domain.Tokens{}, fmt.Errorf("rotate refresh token: %w", err)
	}

	access, accessExpiresAt, err := a.tokens.Generate(result.UserID, result.Username, false)
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
	case err == nil:
		a.metrics.SessionsRevoked(ReasonLogout, 1)
		return nil
	case errors.Is(err, apperr.ErrNotFound):
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
	}, user.CredentialsVersion); err != nil {
		if errors.Is(err, apperr.ErrConflict) {
			return domain.Tokens{}, fmt.Errorf(
				"%w: the password changed while this login was in progress",
				apperr.ErrInvalidCredentials)
		}
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
