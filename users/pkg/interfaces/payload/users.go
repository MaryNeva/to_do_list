package payload

import (
	"time"

	"to-do-list.com/users/pkg/domain"
)

type User struct {
	Id        int       `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Password  string    `json:"password"`
	ListTasks []int     `json:"list_tasks"`
	CreateAt  time.Time `json:"create_at"`
	UpdateAt  time.Time `json:"update_at"`
}

func ToUserDomain(user User) domain.User {
	return domain.User{
		Id:        user.Id,
		Username:  user.Username,
		Email:     user.Email,
		Password:  user.Password,
		ListTasks: user.ListTasks,
		CreateAt:  user.CreateAt,
		UpdateAt:  user.UpdateAt,
	}
}

func ToUserPayload(user domain.User) User {
	return User{
		Id:        user.Id,
		Username:  user.Username,
		Email:     user.Email,
		Password:  user.Password,
		ListTasks: user.ListTasks,
		CreateAt:  user.CreateAt,
		UpdateAt:  user.UpdateAt,
	}
}
