package handler

import (
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
	httptransport "to-do-list/internal/transport/http"
	"to-do-list/internal/transport/http/dto"
	"to-do-list/internal/transport/http/middleware"
)

type UserHandler struct {
	users    UserService
	validate *validator.Validate
}

func NewUserHandler(users UserService) *UserHandler {
	return &UserHandler{users: users, validate: newValidator()}
}

func (h *UserHandler) Mount(router fiber.Router) {
	router.Get("/", h.List)
	router.Get("/:id", h.Get)
	router.Patch("/:id", h.Update)
	router.Delete("/:id", h.Delete)
}

func (h *UserHandler) claims(c *fiber.Ctx) (domain.Claims, error) {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return domain.Claims{}, apperr.ErrUnauthorized
	}
	return claims, nil
}

func (h *UserHandler) List(c *fiber.Ctx) error {
	claims, err := h.claims(c)
	if err != nil {
		return err
	}

	page, err := parsePageParams(c)
	if err != nil {
		return err
	}

	users, err := h.users.List(c.UserContext(), claims, page)
	if err != nil {
		return err
	}

	return httptransport.JSON(c, fiber.StatusOK, dto.ToUserPageResponse(users))
}

func (h *UserHandler) Get(c *fiber.Ctx) error {
	claims, err := h.claims(c)
	if err != nil {
		return err
	}

	id, err := parseIDParam(c)
	if err != nil {
		return err
	}

	user, err := h.users.Get(c.UserContext(), claims, id)
	if err != nil {
		return err
	}

	return httptransport.JSON(c, fiber.StatusOK, dto.ToUserResponse(user))
}

func (h *UserHandler) Update(c *fiber.Ctx) error {
	claims, err := h.claims(c)
	if err != nil {
		return err
	}

	id, err := parseIDParam(c)
	if err != nil {
		return err
	}

	var req dto.UpdateUserRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if err := h.validate.Struct(req); err != nil {
		return err
	}

	user, err := h.users.Update(c.UserContext(), claims, id, req.Username, req.Email, req.Password)
	if err != nil {
		return err
	}

	return httptransport.JSON(c, fiber.StatusOK, dto.ToUserResponse(user))
}

func (h *UserHandler) Delete(c *fiber.Ctx) error {
	claims, err := h.claims(c)
	if err != nil {
		return err
	}

	id, err := parseIDParam(c)
	if err != nil {
		return err
	}

	if err := h.users.Delete(c.UserContext(), claims, id); err != nil {
		return err
	}

	return c.SendStatus(fiber.StatusNoContent)
}
