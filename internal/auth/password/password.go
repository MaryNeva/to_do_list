package password

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const MaxLength = 72

type Hasher struct {
	cost int
}

func NewHasher(cost int) (*Hasher, error) {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return nil, fmt.Errorf("password: bcrypt cost must be between %d and %d, got %d", bcrypt.MinCost, bcrypt.MaxCost, cost)
	}
	return &Hasher{cost: cost}, nil
}

// Cost reports the configured bcrypt work factor.
func (h *Hasher) Cost() int { return h.cost }

// Hash bcrypt-hashes a plaintext password.
func (h *Hasher) Hash(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("password: cannot hash an empty password")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(plain), h.cost)
	if err != nil {
		return "", fmt.Errorf("password: hash: %w", err)
	}
	return string(hashed), nil
}

func Verify(hash, plain string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)); err != nil {
		return fmt.Errorf("password: verify: %w", err)
	}
	return nil
}

func Matches(hash, plain string) bool {
	return Verify(hash, plain) == nil
}
