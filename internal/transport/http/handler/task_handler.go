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

type TaskHandler struct {
	tasks    domain.TaskService
	validate *validator.Validate
}

func NewTaskHandler(tasks domain.TaskService) *TaskHandler {
	return &TaskHandler{tasks: tasks, validate: validator.New()}
}

func (h *TaskHandler) Mount(router fiber.Router) {
	router.Post("/", h.Create)
	router.Get("/", h.List)
	router.Get("/:id", h.Get)
	router.Patch("/:id", h.Update)
	router.Delete("/:id", h.Delete)
	router.Post("/:id/toggle-status", h.ToggleStatus)
}

func (h *TaskHandler) claims(c *fiber.Ctx) (domain.Claims, error) {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return domain.Claims{}, apperr.ErrUnauthorized
	}
	return claims, nil
}

func (h *TaskHandler) Create(c *fiber.Ctx) error {
	claims, err := h.claims(c)
	if err != nil {
		return err
	}

	var req dto.CreateTaskRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if err := h.validate.Struct(req); err != nil {
		return err
	}

	task, err := h.tasks.Create(c.Context(), claims.UserID, req.Title, req.Description)
	if err != nil {
		return err
	}

	return httptransport.JSON(c, fiber.StatusCreated, dto.ToTaskResponse(task))
}

func (h *TaskHandler) Get(c *fiber.Ctx) error {
	claims, err := h.claims(c)
	if err != nil {
		return err
	}

	id, err := parseIDParam(c)
	if err != nil {
		return err
	}

	task, err := h.tasks.Get(c.Context(), claims.UserID, id)
	if err != nil {
		return err
	}

	return httptransport.JSON(c, fiber.StatusOK, dto.ToTaskResponse(task))
}

func (h *TaskHandler) List(c *fiber.Ctx) error {
	claims, err := h.claims(c)
	if err != nil {
		return err
	}

	filter, err := parseTaskFilter(c)
	if err != nil {
		return err
	}

	page, err := h.tasks.List(c.Context(), claims.UserID, filter)
	if err != nil {
		return err
	}

	return httptransport.JSON(c, fiber.StatusOK, dto.ToTaskPageResponse(page))
}

func (h *TaskHandler) Update(c *fiber.Ctx) error {
	claims, err := h.claims(c)
	if err != nil {
		return err
	}

	id, err := parseIDParam(c)
	if err != nil {
		return err
	}

	var req dto.UpdateTaskRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if err := h.validate.Struct(req); err != nil {
		return err
	}

	task, err := h.tasks.Update(c.Context(), claims.UserID, id, req.Title, req.Description)
	if err != nil {
		return err
	}

	return httptransport.JSON(c, fiber.StatusOK, dto.ToTaskResponse(task))
}

func (h *TaskHandler) Delete(c *fiber.Ctx) error {
	claims, err := h.claims(c)
	if err != nil {
		return err
	}

	id, err := parseIDParam(c)
	if err != nil {
		return err
	}

	if err := h.tasks.Delete(c.Context(), claims.UserID, id); err != nil {
		return err
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func (h *TaskHandler) ToggleStatus(c *fiber.Ctx) error {
	claims, err := h.claims(c)
	if err != nil {
		return err
	}

	id, err := parseIDParam(c)
	if err != nil {
		return err
	}

	task, err := h.tasks.ToggleStatus(c.Context(), claims.UserID, id)
	if err != nil {
		return err
	}

	return httptransport.JSON(c, fiber.StatusOK, dto.ToTaskResponse(task))
}
