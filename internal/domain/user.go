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

// UserEdit is a partial profile edit with a plain-text password. A nil field
// is unchanged; a present empty value is invalid because no field can be cleared.
type UserEdit struct {
	Username *string
	Email    *string
	Password *string
}

func (e UserEdit) IsEmpty() bool {
	return e.Username == nil && e.Email == nil && e.Password == nil
}

// UserUpdate is a validated UserEdit ready for storage. A non-nil
// PasswordHash also increments credentials_version.
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
