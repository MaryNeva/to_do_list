package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

type claims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin,omitempty"`
	jwt.RegisteredClaims
}

type Service struct {
	secret []byte
	ttl    time.Duration
	issuer string
}

func NewService(secret string, ttl time.Duration, issuer string, minSecretLength int) (*Service, error) {
	if minSecretLength <= 0 {
		return nil, fmt.Errorf("token: minimum secret length must be positive, got %d", minSecretLength)
	}
	if len(secret) < minSecretLength {
		return nil, fmt.Errorf("token: secret must be at least %d characters, got %d", minSecretLength, len(secret))
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("token: ttl must be positive, got %s", ttl)
	}
	return &Service{secret: []byte(secret), ttl: ttl, issuer: issuer}, nil
}

func (s *Service) Generate(userID int64, username string, isAdmin bool) (tokenString string, expiresAt time.Time, err error) {
	now := time.Now()
	expiresAt = now.Add(s.ttl)

	c := claims{
		UserID:   userID,
		Username: username,
		IsAdmin:  isAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token: sign: %w", err)
	}

	return raw, expiresAt, nil
}

func (s *Service) Parse(tokenString string) (domain.Claims, error) {
	if tokenString == "" {
		return domain.Claims{}, fmt.Errorf("%w: empty token", apperr.ErrUnauthorized)
	}

	var c claims
	parsed, err := jwt.ParseWithClaims(tokenString, &c, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", t.Header["alg"])
		}
		return s.secret, nil
	})

	if err != nil {
		var verr *jwt.ValidationError
		if errors.As(err, &verr) {
			switch {
			case verr.Errors&jwt.ValidationErrorExpired != 0:
				return domain.Claims{}, fmt.Errorf("%w: token expired", apperr.ErrUnauthorized)
			case verr.Errors&jwt.ValidationErrorNotValidYet != 0:
				return domain.Claims{}, fmt.Errorf("%w: token not valid yet", apperr.ErrUnauthorized)
			case verr.Errors&jwt.ValidationErrorSignatureInvalid != 0:
				return domain.Claims{}, fmt.Errorf("%w: invalid token signature", apperr.ErrUnauthorized)
			}
		}
		return domain.Claims{}, fmt.Errorf("%w: invalid token", apperr.ErrUnauthorized)
	}

	if !parsed.Valid {
		return domain.Claims{}, fmt.Errorf("%w: invalid token", apperr.ErrUnauthorized)
	}

	if c.UserID < 0 {
		return domain.Claims{}, fmt.Errorf("%w: invalid token subject", apperr.ErrUnauthorized)
	}

	return domain.Claims{
		UserID:   c.UserID,
		Username: c.Username,
		IsAdmin:  c.IsAdmin,
	}, nil
}
