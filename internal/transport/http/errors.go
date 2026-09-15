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

func NewErrorHandler(logger *slog.Logger) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		requestID := c.GetRespHeader(fiber.HeaderXRequestID)

		var fiberErr *fiber.Error
		if errors.As(err, &fiberErr) {
			return JSON(c, fiberErr.Code, errorResponse{Error: fiberErr.Message, RequestID: requestID})
		}

		var validationErrs validator.ValidationErrors
		if errors.As(err, &validationErrs) {
			return JSON(c, fiber.StatusBadRequest, errorResponse{
				Error:     formatValidationErrors(validationErrs),
				RequestID: requestID,
			})
		}

		switch {
		case errors.Is(err, apperr.ErrNotFound):
			return JSON(c, fiber.StatusNotFound, errorResponse{Error: err.Error(), RequestID: requestID})
		case errors.Is(err, apperr.ErrConflict):
			return JSON(c, fiber.StatusConflict, errorResponse{Error: err.Error(), RequestID: requestID})
		case errors.Is(err, apperr.ErrValidation):
			return JSON(c, fiber.StatusBadRequest, errorResponse{Error: err.Error(), RequestID: requestID})
		case errors.Is(err, apperr.ErrInvalidCredentials):
			return JSON(c, fiber.StatusUnauthorized, errorResponse{Error: err.Error(), RequestID: requestID})
		case errors.Is(err, apperr.ErrUnauthorized):
			return JSON(c, fiber.StatusUnauthorized, errorResponse{Error: err.Error(), RequestID: requestID})
		case errors.Is(err, apperr.ErrForbidden):
			return JSON(c, fiber.StatusForbidden, errorResponse{Error: err.Error(), RequestID: requestID})
		}

		logger.LogAttrs(c.Context(), slog.LevelError, "unhandled request error",
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
			slog.String("request_id", requestID),
			slog.String("error", err.Error()),
		)

		return JSON(c, fiber.StatusInternalServerError, errorResponse{
			Error:     "internal server error",
			RequestID: requestID,
		})
	}
}

func formatValidationErrors(errs validator.ValidationErrors) string {
	messages := make([]string, 0, len(errs))
	for _, fe := range errs {
		messages = append(messages, fmt.Sprintf("%s: failed '%s' validation", fe.Field(), fe.Tag()))
	}
	return strings.Join(messages, "; ")
}
