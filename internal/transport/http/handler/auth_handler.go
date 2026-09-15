package handler

import (
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/domain"
	httptransport "to-do-list/internal/transport/http"
	"to-do-list/internal/transport/http/dto"
	"to-do-list/internal/transport/http/middleware"
)

type AuthHandler struct {
	auth     domain.AuthService
	validate *validator.Validate
}

func NewAuthHandler(auth domain.AuthService) *AuthHandler {
	return &AuthHandler{auth: auth, validate: validator.New()}
}

func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req dto.RegisterRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if err := h.validate.Struct(req); err != nil {
		return err
	}

	user, err := h.auth.Register(c.Context(), req.Username, req.Email, req.Password)
	if err != nil {
		return err
	}

	return httptransport.JSON(c, fiber.StatusCreated, dto.ToUserResponse(user))
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req dto.LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if err := h.validate.Struct(req); err != nil {
		return err
	}

	tokenString, expiresAt, _, err := h.auth.Login(c.Context(), req.Username, req.Password)
	if err != nil {
		return err
	}

	return httptransport.JSON(c, fiber.StatusOK, dto.LoginResponse{
		Token:     tokenString,
		TokenType: "Bearer",
		ExpiresAt: expiresAt,
	})
}

func (h *AuthHandler) Me(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "unauthorized")
	}

	return httptransport.JSON(c, fiber.StatusOK, dto.MeResponse{
		UserID:   claims.UserID,
		Username: claims.Username,
		IsAdmin:  claims.IsAdmin,
	})
}
