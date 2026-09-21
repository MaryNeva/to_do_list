package apperr

import "errors"

var (
	// ErrNotFound means the resource does not exist or belongs to someone
	// else; clients cannot tell the two apart.
	ErrNotFound = errors.New("resource not found")

	// ErrConflict means the operation conflicts with stored state: a
	// duplicate or reserved username/email, or a concurrent change.
	ErrConflict = errors.New("resource already exists")

	// ErrInvalidCredentials means a failed login. It does not say whether the
	// username or the password was wrong.
	ErrInvalidCredentials = errors.New("invalid username or password")

	// ErrUnauthorized means a missing, invalid or expired access or refresh token.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden means the caller is authenticated but not allowed to act.
	ErrForbidden = errors.New("forbidden")

	// ErrTokenReuse means a refresh token that was already rotated was
	// presented again while its login's chain is still live: a likely stolen copy.
	ErrTokenReuse = errors.New("refresh token was already used")

	// ErrTokenRevoked means a revoked refresh token was presented whose chain
	// is no longer live (logout, password change, earlier reuse). Unlike
	// reuse, it does not revoke other sessions.
	ErrTokenRevoked = errors.New("refresh token was revoked")

	// ErrValidation means invalid input.
	ErrValidation = errors.New("validation failed")
)
