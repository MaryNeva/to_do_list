package token

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"to-do-list/internal/apperr"
)

const testSecret = "0123456789abcdef0123456789abcdef" // 33 chars, >= MinSecretLength

func newTestService(t *testing.T, ttl time.Duration) *Service {
	t.Helper()
	svc, err := NewService(testSecret, ttl, "to-do-list-test")
	if err != nil {
		t.Fatalf("NewService() unexpected error: %v", err)
	}
	return svc
}

func TestNewService_RejectsWeakSecret(t *testing.T) {
	if _, err := NewService("too-short", time.Hour, "issuer"); err == nil {
		t.Error("NewService() with a short secret should return an error")
	}
}

func TestNewService_RejectsNonPositiveTTL(t *testing.T) {
	if _, err := NewService(testSecret, 0, "issuer"); err == nil {
		t.Error("NewService() with a zero ttl should return an error")
	}
	if _, err := NewService(testSecret, -time.Second, "issuer"); err == nil {
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

	verifier, err := NewService(strings.Repeat("z", MinSecretLength), time.Hour, "issuer")
	if err != nil {
		t.Fatalf("NewService() unexpected error: %v", err)
	}

	if _, err := verifier.Parse(tokenString); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Errorf("Parse() with the wrong secret error = %v, want apperr.ErrUnauthorized", err)
	}
}

// TestParse_RejectsAlgNone guards against the classic JWT "algorithm
// confusion" attack, where a token is signed with alg "none" (no signature
// at all) hoping a lenient verifier accepts it as trusted.
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
