package domain

import (
	"context"
	"time"
)

type RefreshToken struct {
	ID        int64
	UserID    int64
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

func (t RefreshToken) IsUsable(now time.Time) bool {
	return t.RevokedAt == nil && now.Before(t.ExpiresAt)
}

type RotateResult struct {
	Issued RefreshToken
	UserID int64
}

type RefreshTokenRepository interface {
	Create(ctx context.Context, token RefreshToken) (RefreshToken, error)
	GetByHash(ctx context.Context, hash string) (RefreshToken, error)

	Rotate(ctx context.Context, presentedHash string, replacement RefreshToken, now time.Time) (RotateResult, error)

	Revoke(ctx context.Context, hash string) error
	RevokeAllForUser(ctx context.Context, userID int64) error
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}
