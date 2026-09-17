package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const refreshTokenBytes = 32

func NewRefreshToken() (plain, hash string, err error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("token: generate refresh token: %w", err)
	}

	plain = base64.RawURLEncoding.EncodeToString(buf)
	return plain, HashRefreshToken(plain), nil
}

func HashRefreshToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func ConstantTimeEquals(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

type Issuer struct{}

func NewIssuer() Issuer { return Issuer{} }

func (Issuer) NewRefreshToken() (plain, hash string, err error) { return NewRefreshToken() }

func (Issuer) HashRefreshToken(plain string) string { return HashRefreshToken(plain) }
