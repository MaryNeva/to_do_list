package password

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const MaxLength = 72

var ErrMismatch = errors.New("password: does not match")

type Hasher struct {
	cost  int
	slots chan struct{}
}

func NewHasher(cost, maxConcurrent int) (*Hasher, error) {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return nil, fmt.Errorf("password: bcrypt cost must be between %d and %d, got %d", bcrypt.MinCost, bcrypt.MaxCost, cost)
	}
	if maxConcurrent < 1 {
		return nil, fmt.Errorf("password: max concurrent hashes must be at least 1, got %d", maxConcurrent)
	}
	return &Hasher{cost: cost, slots: make(chan struct{}, maxConcurrent)}, nil
}

// Cost reports the configured bcrypt work factor.
func (h *Hasher) Cost() int { return h.cost }

// MaxConcurrent reports how many hashes may run at once.
func (h *Hasher) MaxConcurrent() int { return cap(h.slots) }

func (h *Hasher) acquire(ctx context.Context) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("password: waiting for a hashing slot: %w", err)
	}

	select {
	case h.slots <- struct{}{}:
		return func() { <-h.slots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("password: waiting for a hashing slot: %w", ctx.Err())
	}
}

// Hash bcrypt-hashes a plaintext password.
func (h *Hasher) Hash(ctx context.Context, plain string) (string, error) {
	if plain == "" {
		return "", errors.New("password: cannot hash an empty password")
	}

	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	hashed, err := bcrypt.GenerateFromPassword([]byte(plain), h.cost)
	if err != nil {
		return "", fmt.Errorf("password: hash: %w", err)
	}
	return string(hashed), nil
}

func (h *Hasher) Verify(ctx context.Context, hash, plain string) error {
	release, err := h.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	switch err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)); {
	case err == nil:
		return nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		return ErrMismatch
	default:
		return fmt.Errorf("password: stored hash is unusable: %w", err)
	}
}

func ValidHash(hash string) error {
	if _, err := bcrypt.Cost([]byte(hash)); err != nil {
		return fmt.Errorf("password: not a bcrypt hash: %w", err)
	}
	return nil
}
