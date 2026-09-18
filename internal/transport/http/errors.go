package http

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
)

func StatusFor(err error) int {
	if err == nil {
		return fiber.StatusOK
	}

	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return fiberErr.Code
	}

	var validationErrs validator.ValidationErrors
	if errors.As(err, &validationErrs) {
		return fiber.StatusBadRequest
	}

	switch {
	case errors.Is(err, apperr.ErrNotFound):
		return fiber.StatusNotFound
	case errors.Is(err, apperr.ErrConflict):
		return fiber.StatusConflict
	case errors.Is(err, apperr.ErrValidation):
		return fiber.StatusBadRequest
	case errors.Is(err, apperr.ErrInvalidCredentials), errors.Is(err, apperr.ErrUnauthorized):
		return fiber.StatusUnauthorized
	case errors.Is(err, apperr.ErrForbidden):
		return fiber.StatusForbidden
	default:
		return fiber.StatusInternalServerError
	}
}

func NewErrorHandler(logger *slog.Logger) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		requestID := c.GetRespHeader(fiber.HeaderXRequestID)
		status := StatusFor(err)

		if status != fiber.StatusInternalServerError {
			return JSON(c, status, errorResponse{
				Error:     clientMessage(err),
				RequestID: requestID,
			})
		}

		logger.LogAttrs(c.UserContext(), slog.LevelError, "unhandled request error",
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
			slog.String("error", err.Error()),
		)

		return JSON(c, fiber.StatusInternalServerError, errorResponse{
			Error:     "internal server error",
			RequestID: requestID,
		})
	}
}

func clientMessage(err error) string {
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return fiberErr.Message
	}

	var validationErrs validator.ValidationErrors
	if errors.As(err, &validationErrs) {
		return formatValidationErrors(validationErrs)
	}

	return err.Error()
}

func formatValidationErrors(errs validator.ValidationErrors) string {
	messages := make([]string, 0, len(errs))
	for _, fe := range errs {
		messages = append(messages, fmt.Sprintf("%s: failed '%s' validation", fe.Field(), fe.Tag()))
	}
	return strings.Join(messages, "; ")
}
