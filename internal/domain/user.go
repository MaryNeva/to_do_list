package domain

import (
	"context"
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

type UserRepository interface {
	Create(ctx context.Context, user User) (User, error)
	GetByID(ctx context.Context, id int64) (User, error)
	GetByUsername(ctx context.Context, username string) (User, error)
	List(ctx context.Context, page PageRequest) (Page[User], error)
	Update(ctx context.Context, id int64, fields UserUpdate) (User, error)
	UpdateAndRevokeSessions(ctx context.Context, id int64, fields UserUpdate) (User, int64, error)

	Delete(ctx context.Context, id int64) error
}

type UserService interface {
	Get(ctx context.Context, id int64) (User, error)
	List(ctx context.Context, page PageRequest) (Page[User], error)
	Update(ctx context.Context, id int64, username, email, newPassword string) (User, error)
	Delete(ctx context.Context, id int64) error
}

type Tokens struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

type AuthService interface {
	Register(ctx context.Context, username, email, password string) (User, error)
	Login(ctx context.Context, username, password string) (Tokens, User, error)
	Refresh(ctx context.Context, refreshToken string) (Tokens, error)
	Logout(ctx context.Context, refreshToken string) error
	ValidateToken(ctx context.Context, token string) (Claims, error)
}
