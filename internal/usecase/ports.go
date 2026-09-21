package usecase

import (
	"context"
	"time"

	"to-do-list/internal/domain"
)

type AuthUsers interface {
	Create(context.Context, domain.User) (domain.User, error)
	GetByUsername(context.Context, string) (domain.User, error)
}
type UserReader interface {
	GetByID(context.Context, int64) (domain.User, error)
	List(context.Context, domain.PageRequest) (domain.Page[domain.User], error)
}
type ProfileWriter interface {
	Update(context.Context, int64, domain.UserUpdate) (domain.User, error)

	// UpdateAndRevokeSessions applies the update and revokes all of the user's
	// refresh tokens in one transaction. It returns the number revoked.
	UpdateAndRevokeSessions(context.Context, int64, domain.UserUpdate) (domain.User, int64, error)

	Delete(context.Context, int64) error
}
type UserRepository interface {
	UserReader
	ProfileWriter
}
type SessionStore interface {
	// Create stores the token only if the user's credentials_version still
	// equals the given one. apperr.ErrConflict means it changed;
	// apperr.ErrNotFound means the user is gone.
	Create(context.Context, domain.RefreshToken, int64) (domain.RefreshToken, error)

	// Rotate revokes the presented token and inserts the replacement in one
	// transaction under a lock on the user row, so a token is spent at most
	// once. Expiry uses the database clock.
	//
	// Errors:
	//   - ErrTokenReuse: the token was already rotated and its chain still has a
	//     live token. All of the user's tokens are revoked and committed; the
	//     count is in RotateResult.SessionsRevoked.
	//   - ErrTokenRevoked: revoked, and its chain is no longer live.
	//   - ErrNotFound: unknown token or deleted user.
	//   - ErrUnauthorized: the token has expired.
	// Only ErrTokenReuse writes anything. A hash collision is an internal error.
	Rotate(context.Context, string, domain.RefreshToken) (domain.RotateResult, error)

	// Revoke returns apperr.ErrNotFound if no active token has the given hash.
	Revoke(context.Context, string) error

	// RevokeAllForUser returns the number of tokens revoked.
	RevokeAllForUser(context.Context, int64) (int64, error)
}
type ExpiredSessions interface {
	DeleteExpired(context.Context, time.Time) (int64, error)
}
type TaskRepository interface {
	Create(ctx context.Context, task domain.Task) (domain.Task, error)
	GetByID(ctx context.Context, id int64) (domain.Task, error)
	ListByCreator(ctx context.Context, creatorID int64, filter domain.TaskFilter) (domain.Page[domain.Task], error)
	// Update writes only the non-nil fields. A missing task or one owned by
	// someone else is apperr.ErrNotFound.
	Update(ctx context.Context, id, ownerID int64, update domain.TaskUpdate) (domain.Task, error)
	Delete(ctx context.Context, id int64) error
	// CompareAndSetStatus sets the status only if it still equals from.
	// apperr.ErrConflict means it changed concurrently.
	CompareAndSetStatus(ctx context.Context, id, ownerID int64, from, to domain.TaskStatus) (domain.Task, error)
}
