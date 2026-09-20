package apperr

import "errors"

var (
	// ErrNotFound means the requested resource does not exist, or the
	// caller is not allowed to know that it exists (e.g. another user's
	// task) - both cases are deliberately indistinguishable to the client.
	ErrNotFound = errors.New("resource not found")

	// ErrConflict means the operation would violate a uniqueness
	// constraint (duplicate username/email, ...).
	ErrConflict = errors.New("resource already exists")

	// ErrInvalidCredentials means a login attempt failed. It is
	// intentionally generic so responses never reveal whether the
	// username or the password was wrong.
	ErrInvalidCredentials = errors.New("invalid username or password")

	// ErrUnauthorized means the request has no (or an invalid/expired)
	// authentication token.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden means the caller is authenticated but not allowed to
	// perform the requested action on the resource.
	ErrForbidden = errors.New("forbidden")

	// ErrTokenReuse means a refresh token that had already been consumed was
	// presented again. It is deliberately not ErrConflict: that one also
	// covers a newly generated token colliding with a stored hash, which is
	// a generator problem and must not be answered by ending every session
	// the account has.
	ErrTokenReuse = errors.New("refresh token was already used")

	// ErrValidation means the caller supplied malformed input.
	ErrValidation = errors.New("validation failed")
)
