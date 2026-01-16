package domain

import (
	"context"
	"time"
)

type Token string

type AuthRequest struct {
	Username string
	Password string
}
type User struct {
	Id        int
	Username  string
	Email     string
	Password  string
	ListTasks []int
	CreateAt  time.Time
	UpdateAt  time.Time
}

type UserUC interface {
	GetUser(ctx context.Context, id int) (User, error)
	CreateUser(ctx context.Context, user User) (User, error)
	UpdateUser(ctx context.Context, user User) (User, error)
	DeleteUser(ctx context.Context, id int) error
}

type UserStore interface {
	GetUser(ctx context.Context, id int) (User, error)
	CreateUser(ctx context.Context, user User) (User, error)
	UpdateUser(ctx context.Context, user User) error
	DeleteUser(ctx context.Context, id int) error
	LoginUser(ctx context.Context, username string) (User, error)
}

type AuthService interface {
	Login(ctx context.Context, auth AuthRequest) (user User, err error)
	Authenticate(ctx context.Context, auth AuthRequest) (Token, error)
	ValidateToken(ctx context.Context, token Token) (User, error)
}
