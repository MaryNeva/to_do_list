package payload

import (
	"time"

	"github.com/go-playground/validator/v10"
	"to-do-list.com/users/pkg/domain"
)

type AuthRequest struct {
	Username string `json:"username" validate:"required,min=3,max=50"`
	Password string `json:"password" validate:"required,min=8,max=32"`
}

type User struct {
	Id        int       `json:"id"`
	Username  string    `json:"username" validate:"omitempty,min=3,max=50"`
	Email     string    `json:"email"  `
	Password  string    `json:"password" validate:"omitempty,min=8,max=32"`
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

func Validate(req any) error {
	validate := validator.New()

	err := validate.Struct(req)
	if err != nil {
		return err
	}

	return nil
}

func RequestToAuth(u AuthRequest) domain.AuthRequest {
	return domain.AuthRequest{
		Username: u.Username,
		Password: u.Password,
	}
}

type AuthToken struct {
	Token domain.Token `json:"token"`
}

type AuthValidate struct {
	AuthToken AuthToken `json:"authToken"`
	User      User      `json:"user"`
}

func FromDTOToken(a AuthToken) domain.Token {
	return a.Token
}
