package domain

import (
	"context"
	"time"
)

type Token string
type User struct {
	Id        int
	Username  string
	Email     string
	Password  string
	ListTasks []int
	CreateAt  time.Time `json:"create_at"`
	UpdateAt  time.Time `json:"update_at"`
}

type (
	GetUser    func(ctx context.Context, id int) (User, error)
	CreateUser func(ctx context.Context, user User) (User, error)
	UpdateUser func(ctx context.Context, user User) (User, error)
	DeleteUser func(ctx context.Context, id int) error
)

type UserStore interface {
	GetUser(ctx context.Context, id int) (User, error)
	CreateUser(ctx context.Context, user User) (User, error)
	UpdateUser(ctx context.Context, user User) error
	DeleteUser(ctx context.Context, id int) error
}
