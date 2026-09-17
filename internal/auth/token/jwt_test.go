package token

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"to-do-list/internal/apperr"
)

const (
	testSecret          = "0123456789abcdef0123456789abcdef" // 32 chars
	testMinSecretLength = 32
)

func newTestService(t *testing.T, ttl time.Duration) *Service {
	t.Helper()
	svc, err := NewService(testSecret, ttl, "to-do-list-test", testMinSecretLength)
	if err != nil {
		t.Fatalf("NewService() unexpected error: %v", err)
	}
	return svc
}

func TestNewService_RejectsWeakSecret(t *testing.T) {
	if _, err := NewService("too-short", time.Hour, "issuer", testMinSecretLength); err == nil {
		t.Error("NewService() with a short secret should return an error")
	}
}

func TestNewService_RejectsNonPositiveTTL(t *testing.T) {
	if _, err := NewService(testSecret, 0, "issuer", testMinSecretLength); err == nil {
		t.Error("NewService() with a zero ttl should return an error")
	}
	if _, err := NewService(testSecret, -time.Second, "issuer", testMinSecretLength); err == nil {
		t.Error("NewService() with a negative ttl should return an error")
	}
}

func TestGenerateAndParse_RoundTrip(t *testing.T) {
	svc := newTestService(t, time.Hour)

	tokenString, expiresAt, err := svc.Generate(42, "alice", false)
	if err != nil {
		t.Fatalf("Generate() unexpected error: %v", err)
	}
	if tokenString == "" {
		t.Fatal("Generate() returned an empty token")
	}
	if !expiresAt.After(time.Now()) {
		t.Errorf("Generate() expiresAt = %v, want a time in the future", expiresAt)
	}

	claims, err := svc.Parse(tokenString)
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if claims.UserID != 42 {
		t.Errorf("Parse() UserID = %d, want 42", claims.UserID)
	}
	if claims.Username != "alice" {
		t.Errorf("Parse() Username = %q, want %q", claims.Username, "alice")
	}
	if claims.IsAdmin {
		t.Error("Parse() IsAdmin = true, want false")
	}
}

func TestGenerate_AdminFlagRoundTrips(t *testing.T) {
	svc := newTestService(t, time.Hour)

	tokenString, _, err := svc.Generate(0, "admin", true)
	if err != nil {
		t.Fatalf("Generate() unexpected error: %v", err)
	}

	claims, err := svc.Parse(tokenString)
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if !claims.IsAdmin {
		t.Error("Parse() IsAdmin = false, want true for an admin token")
	}
}

func TestParse_EmptyToken(t *testing.T) {
	svc := newTestService(t, time.Hour)

	if _, err := svc.Parse(""); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("Parse(\"\") error = %v, want apperr.ErrUnauthorized", err)
	}
}

func TestParse_MalformedToken(t *testing.T) {
	svc := newTestService(t, time.Hour)

	if _, err := svc.Parse("not-a-jwt-at-all"); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("Parse() error = %v, want apperr.ErrUnauthorized", err)
	}
}

func TestParse_ExpiredToken(t *testing.T) {
	svc := newTestService(t, time.Millisecond)

	tokenString, _, err := svc.Generate(1, "bob", false)
	if err != nil {
		t.Fatalf("Generate() unexpected error: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if _, err := svc.Parse(tokenString); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("Parse() on an expired token error = %v, want apperr.ErrUnauthorized", err)
	}
}

func TestParse_TamperedSignature(t *testing.T) {
	svc := newTestService(t, time.Hour)

	tokenString, _, err := svc.Generate(1, "bob", false)
	if err != nil {
		t.Fatalf("Generate() unexpected error: %v", err)
	}

	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		t.Fatalf("generated token has %d parts, want 3", len(parts))
	}
	// Flip the signature so it no longer matches header+payload.
	tampered := parts[0] + "." + parts[1] + "." + parts[2] + "tamper"

	if _, err := svc.Parse(tampered); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("Parse() on a tampered token error = %v, want apperr.ErrUnauthorized", err)
	}
}

func TestParse_WrongSecret(t *testing.T) {
	issuer := newTestService(t, time.Hour)
	tokenString, _, err := issuer.Generate(1, "bob", false)
	if err != nil {
		t.Fatalf("Generate() unexpected error: %v", err)
	}

	verifier, err := NewService(strings.Repeat("z", testMinSecretLength), time.Hour, "issuer", testMinSecretLength)
	if err != nil {
		t.Fatalf("NewService() unexpected error: %v", err)
	}

	if _, err := verifier.Parse(tokenString); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("Parse() with the wrong secret error = %v, want apperr.ErrUnauthorized", err)
	}
}

func TestParse_RejectsAlgNone(t *testing.T) {
	svc := newTestService(t, time.Hour)

	unsafeClaims := claims{
		UserID:   999,
		Username: "attacker",
		IsAdmin:  true,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}

	noneToken := jwt.NewWithClaims(jwt.SigningMethodNone, unsafeClaims)
	tokenString, err := noneToken.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("failed to build the alg=none fixture token: %v", err)
	}

	if _, err := svc.Parse(tokenString); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("Parse() on an alg=none token error = %v, want apperr.ErrUnauthorized (algorithm confusion not blocked!)", err)
	}
}

func signedWith(t *testing.T, method jwt.SigningMethod, secret string, c claims) string {
	t.Helper()
	tokenString, err := jwt.NewWithClaims(method, c).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tokenString
}

func validClaims() claims {
	now := time.Now()
	return claims{
		UserID:   42,
		Username: "alice",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "alice",
			Issuer:    "to-do-list-test",
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	}
}

func TestParse_RejectsWellSignedButUnacceptableTokens(t *testing.T) {
	svc := newTestService(t, time.Hour)

	withClaims := func(mutate func(*claims)) claims {
		c := validClaims()
		mutate(&c)
		return c
	}

	tests := []struct {
		name   string
		method jwt.SigningMethod
		claims claims
	}{
		{
			name:   "issuer belongs to another service",
			method: jwt.SigningMethodHS256,
			claims: withClaims(func(c *claims) { c.Issuer = "another-service" }),
		},
		{
			name:   "issuer missing entirely",
			method: jwt.SigningMethodHS256,
			claims: withClaims(func(c *claims) { c.Issuer = "" }),
		},
		{
			name:   "no expiry, so the token would never lapse",
			method: jwt.SigningMethodHS256,
			claims: withClaims(func(c *claims) { c.ExpiresAt = nil }),
		},
		{
			name:   "already expired",
			method: jwt.SigningMethodHS256,
			claims: withClaims(func(c *claims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) }),
		},
		{
			name:   "signed with a different HMAC algorithm",
			method: jwt.SigningMethodHS512,
			claims: validClaims(),
		},
		{
			name:   "user id 0 without the admin flag",
			method: jwt.SigningMethodHS256,
			claims: withClaims(func(c *claims) { c.UserID = 0; c.IsAdmin = false }),
		},
		{
			name:   "negative user id",
			method: jwt.SigningMethodHS256,
			claims: withClaims(func(c *claims) { c.UserID = -1 }),
		},
		{
			name:   "no username",
			method: jwt.SigningMethodHS256,
			claims: withClaims(func(c *claims) { c.Username = "  " }),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Every token here carries a valid signature made with the
			// service's own secret: only the claims or the algorithm differ.
			tokenString := signedWith(t, tt.method, testSecret, tt.claims)

			got, err := svc.Parse(tokenString)
			if !errors.Is(err, apperr.ErrUnauthorized) {
				t.Fatalf("Parse() = %+v, err = %v, want apperr.ErrUnauthorized", got, err)
			}
		})
	}
}

func TestParse_AcceptsTheBootstrapAdminWithIDZero(t *testing.T) {
	svc := newTestService(t, time.Hour)

	c := validClaims()
	c.UserID = 0
	c.Username = "admin"
	c.IsAdmin = true

	claimsOut, err := svc.Parse(signedWith(t, jwt.SigningMethodHS256, testSecret, c))
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if claimsOut.UserID != 0 || !claimsOut.IsAdmin {
		t.Errorf("Parse() = %+v, want the admin identity with id 0", claimsOut)
	}
}

func TestGenerateAndParse_IssuerRoundTrips(t *testing.T) {
	svc := newTestService(t, time.Hour)

	tokenString, _, err := svc.Generate(7, "alice", false)
	if err != nil {
		t.Fatalf("Generate() unexpected error: %v", err)
	}

	if _, err := svc.Parse(tokenString); err != nil {
		t.Fatalf("a token this service issued must verify: %v", err)
	}

	other, err := NewService(testSecret, time.Hour, "some-other-service", testMinSecretLength)
	if err != nil {
		t.Fatalf("NewService(): %v", err)
	}
	if _, err := other.Parse(tokenString); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("a service with a different issuer must reject the token, got: %v", err)
	}
}
