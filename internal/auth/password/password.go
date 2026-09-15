package password

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const MaxLength = 72

const Cost = 12

// Hash bcrypt-hashes a plaintext password.
func Hash(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("password: cannot hash an empty password")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(plain), Cost)
	if err != nil {
		return "", fmt.Errorf("password: hash: %w", err)
	}
	return string(hashed), nil
}

// Verify reports an error if plain does not match hash.
func Verify(hash, plain string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)); err != nil {
		return fmt.Errorf("password: verify: %w", err)
	}
	return nil
}

// Matches is a convenience boolean wrapper around Verify for call sites that
// don't care about the specific error.
func Matches(hash, plain string) bool {
	return Verify(hash, plain) == nil
}
