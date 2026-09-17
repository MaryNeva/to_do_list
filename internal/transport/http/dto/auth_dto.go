package dto

import (
	"time"

	"to-do-list/internal/domain"
)

type RegisterRequest struct {
	Username string `json:"username" validate:"required"`
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

type LoginRequest struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
}

type LoginResponse struct {
	Token            string     `json:"token"`
	TokenType        string     `json:"token_type"`
	ExpiresAt        time.Time  `json:"expires_at"`
	RefreshToken     string     `json:"refresh_token,omitempty"`
	RefreshExpiresAt *time.Time `json:"refresh_expires_at,omitempty"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

func ToLoginResponse(tokens domain.Tokens) LoginResponse {
	resp := LoginResponse{
		Token:     tokens.AccessToken,
		TokenType: "Bearer",
		ExpiresAt: tokens.AccessExpiresAt,
	}
	if tokens.RefreshToken != "" {
		expiresAt := tokens.RefreshExpiresAt
		resp.RefreshToken = tokens.RefreshToken
		resp.RefreshExpiresAt = &expiresAt
	}
	return resp
}

type MeResponse struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}
