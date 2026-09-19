package domain

import (
	"time"
)

type User struct {
	ID                 int64
	Username           string
	Email              string
	PasswordHash       string
	CredentialsVersion int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type Claims struct {
	UserID   int64
	Username string
	IsAdmin  bool
}

type UserUpdate struct {
	Username     *string
	Email        *string
	PasswordHash *string
}

type Tokens struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}
