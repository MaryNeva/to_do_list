package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

type fakeRefreshRepo struct {
	mu     sync.Mutex
	tokens map[string]domain.RefreshToken
	nextID int64

	// rotated marks hashes revoked by rotation (revoked_reason = 'rotated');
	// family maps each hash to its login's chain (family_id). Reuse needs both
	// a rotated token and a live token in its family, as in Postgres.
	rotated    map[string]bool
	family     map[string]int64
	nextFamily int64

	rotateErr    error
	revokeAllErr error
	failInsert   bool

	lookupUsername            func(userID int64) string
	currentCredentialsVersion func(userID int64) int64
}

func newFakeRefreshRepo() *fakeRefreshRepo {
	return &fakeRefreshRepo{
		tokens:  make(map[string]domain.RefreshToken),
		rotated: make(map[string]bool),
		family:  make(map[string]int64),
		nextID:  1,
	}
}

func (f *fakeRefreshRepo) Create(_ context.Context, token domain.RefreshToken, credentialsVersion int64) (domain.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.currentCredentialsVersion != nil && f.currentCredentialsVersion(token.UserID) != credentialsVersion {
		return domain.RefreshToken{}, apperr.ErrConflict
	}

	if _, exists := f.tokens[token.TokenHash]; exists {
		return domain.RefreshToken{}, apperr.ErrConflict
	}
	token.ID = f.nextID
	f.nextID++
	token.CreatedAt = time.Now()
	f.tokens[token.TokenHash] = token
	f.nextFamily++
	f.family[token.TokenHash] = f.nextFamily
	return token, nil
}

func (f *fakeRefreshRepo) GetByHash(_ context.Context, hash string) (domain.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	token, ok := f.tokens[hash]
	if !ok {
		return domain.RefreshToken{}, apperr.ErrNotFound
	}
	return token, nil
}

func (f *fakeRefreshRepo) Rotate(
	_ context.Context,
	presentedHash string,
	replacement domain.RefreshToken,
) (domain.RotateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now()

	if f.rotateErr != nil {
		return domain.RotateResult{}, f.rotateErr
	}

	presented, ok := f.tokens[presentedHash]
	if !ok {
		return domain.RotateResult{}, apperr.ErrNotFound
	}
	if presented.RevokedAt != nil && (!f.rotated[presentedHash] || !f.familyLiveLocked(f.family[presentedHash], now)) {
		return domain.RotateResult{UserID: presented.UserID, Username: f.usernameOf(presented.UserID)},
			apperr.ErrTokenRevoked
	}
	if presented.RevokedAt != nil {
		revoked, err := f.revokeAllLocked(presented.UserID)
		if err != nil {
			return domain.RotateResult{}, fmt.Errorf("revoke sessions after refresh token reuse: %w", err)
		}
		return domain.RotateResult{
			UserID:          presented.UserID,
			Username:        f.usernameOf(presented.UserID),
			SessionsRevoked: revoked,
		}, apperr.ErrTokenReuse
	}
	if !now.Before(presented.ExpiresAt) {
		return domain.RotateResult{UserID: presented.UserID, Username: f.usernameOf(presented.UserID)},
			fmt.Errorf("%w: refresh token expired", apperr.ErrUnauthorized)
	}

	if f.failInsert {
		return domain.RotateResult{}, errors.New("cannot store the replacement token")
	}

	consumedAt := now
	presented.RevokedAt = &consumedAt
	f.tokens[presentedHash] = presented
	f.rotated[presentedHash] = true

	replacement.ID = f.nextID
	f.nextID++
	replacement.UserID = presented.UserID
	replacement.CreatedAt = now
	f.tokens[replacement.TokenHash] = replacement
	f.family[replacement.TokenHash] = f.family[presentedHash]

	return domain.RotateResult{
		Issued:   replacement,
		UserID:   presented.UserID,
		Username: f.usernameOf(presented.UserID),
	}, nil
}

// familyLiveLocked expects f.mu to be held.
func (f *fakeRefreshRepo) familyLiveLocked(family int64, now time.Time) bool {
	for hash, token := range f.tokens {
		if f.family[hash] == family && token.RevokedAt == nil && now.Before(token.ExpiresAt) {
			return true
		}
	}
	return false
}

func (f *fakeRefreshRepo) liveSessionsOf(userID int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	live := 0
	for _, token := range f.tokens {
		if token.UserID == userID && token.RevokedAt == nil {
			live++
		}
	}
	return live
}

func (f *fakeRefreshRepo) usernameOf(userID int64) string {
	if f.lookupUsername == nil {
		return ""
	}
	return f.lookupUsername(userID)
}

func (f *fakeRefreshRepo) Revoke(_ context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	token, ok := f.tokens[hash]
	if !ok || token.RevokedAt != nil {
		return apperr.ErrNotFound
	}
	now := time.Now()
	token.RevokedAt = &now
	f.tokens[hash] = token
	return nil
}

func (f *fakeRefreshRepo) RevokeAllForUser(_ context.Context, userID int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.revokeAllLocked(userID)
}

// revokeAllLocked expects f.mu to be held; it models "in the same transaction".
func (f *fakeRefreshRepo) revokeAllLocked(userID int64) (int64, error) {
	if f.revokeAllErr != nil {
		return 0, f.revokeAllErr
	}

	now := time.Now()
	var revoked int64
	for hash, token := range f.tokens {
		if token.UserID == userID && token.RevokedAt == nil {
			token.RevokedAt = &now
			f.tokens[hash] = token
			revoked++
		}
	}
	return revoked, nil
}

func (f *fakeRefreshRepo) DeleteExpired(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var removed int64
	for hash, token := range f.tokens {
		if token.ExpiresAt.Before(before) {
			delete(f.tokens, hash)
			removed++
		}
	}
	return removed, nil
}

type fakeIssuer struct {
	mu      sync.Mutex
	counter int
	err     error
}

func (f *fakeIssuer) NewRefreshToken() (string, string, error) {
	if f.err != nil {
		return "", "", f.err
	}
	f.mu.Lock()
	f.counter++
	plain := fmt.Sprintf("refresh-token-%d", f.counter)
	f.mu.Unlock()
	return plain, f.HashRefreshToken(plain), nil
}

func (f *fakeIssuer) HashRefreshToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
