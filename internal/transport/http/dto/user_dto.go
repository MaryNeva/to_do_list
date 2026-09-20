package dto

import (
	"time"

	"to-do-list/internal/auth/password"
	"to-do-list/internal/domain"
)

type UpdateUserRequest struct {
	Username *string `json:"username"`
	Email    *string `json:"email" validate:"omitempty,email"`
	Password *string `json:"password"`
}

func (r UpdateUserRequest) ToDomain() domain.UserEdit {
	return domain.UserEdit{Username: r.Username, Email: r.Email, Password: r.Password}
}

type UserResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func ToUserResponse(u domain.User) UserResponse {
	return UserResponse{
		ID:        u.ID,
		Username:  u.Username,
		Email:     u.Email,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

func ToUserListResponse(users []domain.User) []UserResponse {
	result := make([]UserResponse, 0, len(users))
	for _, u := range users {
		result = append(result, ToUserResponse(u))
	}
	return result
}

const MaxPasswordLength = password.MaxLength
