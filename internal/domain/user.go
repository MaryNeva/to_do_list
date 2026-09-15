package domain

import (
	"context"
	"time"
)

type User struct {
	ID           int64
	Username     string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Claims struct {
	UserID   int64
	Username string
	IsAdmin  bool
}

type UserRepository interface {
	Create(ctx context.Context, user User) (User, error)
	GetByID(ctx context.Context, id int64) (User, error)
	GetByUsername(ctx context.Context, username string) (User, error)
	List(ctx context.Context) ([]User, error)
	Update(ctx context.Context, user User) error
	Delete(ctx context.Context, id int64) error
}

type UserService interface {
	Get(ctx context.Context, id int64) (User, error)
	List(ctx context.Context) ([]User, error)
	Update(ctx context.Context, id int64, username, email, newPassword string) (User, error)
	Delete(ctx context.Context, id int64) error
}

type AuthService interface {
	Register(ctx context.Context, username, email, password string) (User, error)
	Login(ctx context.Context, username, password string) (token string, expiresAt time.Time, user User, err error)
	ValidateToken(ctx context.Context, token string) (Claims, error)
}
