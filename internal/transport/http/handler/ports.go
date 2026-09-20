package handler

import (
	"context"

	"to-do-list/internal/domain"
)

type UserService interface {
	Get(ctx context.Context, actor domain.Claims, id int64) (domain.User, error)
	List(ctx context.Context, actor domain.Claims, page domain.PageRequest) (domain.Page[domain.User], error)
	Update(ctx context.Context, actor domain.Claims, id int64, edit domain.UserEdit) (domain.User, error)
	Delete(ctx context.Context, actor domain.Claims, id int64) error
}
type AuthService interface {
	Register(ctx context.Context, username, email, password string) (domain.User, error)
	Login(ctx context.Context, username, password string) (domain.Tokens, domain.User, error)
	Refresh(ctx context.Context, refreshToken string) (domain.Tokens, error)
	Logout(ctx context.Context, refreshToken string) error
}
type TaskService interface {
	Create(ctx context.Context, actor domain.Claims, title, description string) (domain.Task, error)
	Get(ctx context.Context, actor domain.Claims, id int64) (domain.Task, error)
	List(ctx context.Context, actor domain.Claims, filter domain.TaskFilter) (domain.Page[domain.Task], error)
	Update(ctx context.Context, actor domain.Claims, id int64, update domain.TaskUpdate) (domain.Task, error)
	Delete(ctx context.Context, actor domain.Claims, id int64) error
	ToggleStatus(ctx context.Context, actor domain.Claims, id int64) (domain.Task, error)
}
