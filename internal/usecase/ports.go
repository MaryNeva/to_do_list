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

	// UpdateAndRevokeSessions writes the password and ends every session of
	// the account in one transaction, and returns how many it ended.
	UpdateAndRevokeSessions(context.Context, int64, domain.UserUpdate) (domain.User, int64, error)

	Delete(context.Context, int64) error
}
type UserRepository interface {
	UserReader
	ProfileWriter
}
type SessionStore interface {
	// Create stores a session only while the account's credentials are still
	// at the given version; apperr.ErrConflict means the password changed
	// under the caller.
	Create(context.Context, domain.RefreshToken, int64) (domain.RefreshToken, error)

	// Rotate consumes the presented token and stores the replacement in one
	// transaction, so a token is spent at most once. Expiry is judged by the
	// database clock after the lock is held, not by the caller's.
	//
	// The errors say what happened and what was written:
	//
	//   ErrTokenReuse  - the token had already been consumed. Every session
	//                    of that user was revoked in the same transaction
	//                    that detected the replay, and the count is in
	//                    RotateResult.SessionsRevoked. This is the one error
	//                    that does write.
	//   ErrNotFound    - the token is unknown; nothing was written.
	//   ErrUnauthorized - the token has lapsed; nothing was written.
	//   ErrConflict    - the replacement collided with a token already
	//                    stored, which is a generator failure rather than
	//                    anything the presenter did; nothing was written and
	//                    no session is revoked.
	//
	// After anything but ErrTokenReuse the presented token stays usable.
	Rotate(context.Context, string, domain.RefreshToken) (domain.RotateResult, error)

	// Revoke reports apperr.ErrNotFound unless it ended exactly one session.
	Revoke(context.Context, string) error

	// RevokeAllForUser returns how many sessions it ended.
	RevokeAllForUser(context.Context, int64) (int64, error)
}
type ExpiredSessions interface {
	DeleteExpired(context.Context, time.Time) (int64, error)
}
type TaskRepository interface {
	Create(ctx context.Context, task domain.Task) (domain.Task, error)
	GetByID(ctx context.Context, id int64) (domain.Task, error)
	ListByCreator(ctx context.Context, creatorID int64, filter domain.TaskFilter) (domain.Page[domain.Task], error)
	// Update writes the fields update sets and leaves the rest; a row that
	// does not exist or belongs to someone else is apperr.ErrNotFound.
	Update(ctx context.Context, id, ownerID int64, update domain.TaskUpdate) (domain.Task, error)
	Delete(ctx context.Context, id int64) error
	// CompareAndSetStatus writes only while the row still holds from and
	// belongs to ownerID; apperr.ErrConflict means it moved on meanwhile.
	CompareAndSetStatus(ctx context.Context, id, ownerID int64, from, to domain.TaskStatus) (domain.Task, error)
}
