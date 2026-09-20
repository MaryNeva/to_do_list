package domain

import (
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
	Issued          RefreshToken
	UserID          int64
	Username        string
	SessionsRevoked int64
}
