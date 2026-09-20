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

// UserEdit is a partial profile edit as its caller expressed it: plain
// values, before any hashing. A nil field was not sent and keeps its current
// value.
//
// Unlike a task's description, no profile field can be cleared - a user
// without a username or an email is not a user - so a field that is present
// but empty is a mistake to report, not an absence to ignore. Keeping the
// values in pointers is what lets Update tell those two apart at all.
type UserEdit struct {
	Username *string
	Email    *string
	Password *string
}

func (e UserEdit) IsEmpty() bool {
	return e.Username == nil && e.Email == nil && e.Password == nil
}

// UserUpdate is the same edit as it reaches storage: the password has become
// a hash, and the caller-supplied values have been validated.
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
