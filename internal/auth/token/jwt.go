package token

import (
	"errors"
	"fmt"
	"strings"
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

const signingAlgorithm = "HS256"

// validateIdentity rejects claims that are correctly signed but describe no
// usable account. ID 0 is reserved for the bootstrap admin, which has no row
// in users, so any other token carrying it is malformed.
func validateIdentity(c claims) error {
	switch {
	case c.UserID < 0:
		return fmt.Errorf("%w: token carries a negative user id", apperr.ErrUnauthorized)
	case c.UserID == 0 && !c.IsAdmin:
		return fmt.Errorf("%w: user id 0 is reserved for the bootstrap admin", apperr.ErrUnauthorized)
	case strings.TrimSpace(c.Username) == "":
		return fmt.Errorf("%w: token carries no username", apperr.ErrUnauthorized)
	default:
		return nil
	}
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

	raw, err := jwt.NewWithClaims(jwt.GetSigningMethod(signingAlgorithm), c).SignedString(s.secret)
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
	parser := jwt.NewParser(jwt.WithValidMethods([]string{signingAlgorithm}))
	parsed, err := parser.ParseWithClaims(tokenString, &c, func(t *jwt.Token) (interface{}, error) {
		// WithValidMethods already pins the algorithm; this repeats the
		// check at the point the key is handed out, so a future change to
		// the parser options cannot silently widen what gets verified.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok || t.Method.Alg() != signingAlgorithm {
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

	// An access token without exp never stops being accepted, so a missing
	// expiry is rejected rather than treated as "no deadline".
	if c.ExpiresAt == nil {
		return domain.Claims{}, fmt.Errorf("%w: token has no expiry", apperr.ErrUnauthorized)
	}

	if c.Issuer != s.issuer {
		return domain.Claims{}, fmt.Errorf("%w: token was issued by %q, not %q", apperr.ErrUnauthorized, c.Issuer, s.issuer)
	}

	if err := validateIdentity(c); err != nil {
		return domain.Claims{}, err
	}

	return domain.Claims{
		UserID:   c.UserID,
		Username: c.Username,
		IsAdmin:  c.IsAdmin,
	}, nil
}
