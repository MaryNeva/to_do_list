package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
)

// Error codes are part of the contract: a client may switch on them, and they
// outlive any wording change in the accompanying message.
const (
	CodeValidation         = "validation_error"
	CodeBadRequest         = "bad_request"
	CodeNotFound           = "not_found"
	CodeConflict           = "conflict"
	CodeUnauthorized       = "unauthorized"
	CodeInvalidCredentials = "invalid_credentials"
	CodeForbidden          = "forbidden"
	CodeMethodNotAllowed   = "method_not_allowed"
	CodePayloadTooLarge    = "payload_too_large"
	CodeRateLimited        = "rate_limited"
	CodeUnavailable        = "unavailable"
	CodeInternal           = "internal_error"
)

// retryAfterUnavailable is sent with every 503. One second is not a promise
// that the dependency will be back by then; it is a floor, so that a client
// retrying in a loop does not add its own load to whatever is already slow.
const retryAfterUnavailable = "1"

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
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return fiber.StatusServiceUnavailable

	default:
		return fiber.StatusInternalServerError
	}
}

// CodeFor names the failure. Sentinels are asked first so that a wrapped
// domain error keeps its own code even behind a generic status.
func CodeFor(err error) string {
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		return CodeNotFound
	case errors.Is(err, apperr.ErrConflict):
		return CodeConflict
	case errors.Is(err, apperr.ErrValidation):
		return CodeValidation
	case errors.Is(err, apperr.ErrInvalidCredentials):
		return CodeInvalidCredentials
	case errors.Is(err, apperr.ErrUnauthorized):
		return CodeUnauthorized
	case errors.Is(err, apperr.ErrForbidden):
		return CodeForbidden
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return CodeUnavailable
	}

	var validationErrs validator.ValidationErrors
	if errors.As(err, &validationErrs) {
		return CodeValidation
	}

	switch status := StatusFor(err); {
	case status == fiber.StatusBadRequest:
		return CodeValidation
	case status == fiber.StatusUnauthorized:
		return CodeUnauthorized
	case status == fiber.StatusForbidden:
		return CodeForbidden
	case status == fiber.StatusNotFound:
		return CodeNotFound
	case status == fiber.StatusMethodNotAllowed:
		return CodeMethodNotAllowed
	case status == fiber.StatusConflict:
		return CodeConflict
	case status == fiber.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge
	case status == fiber.StatusTooManyRequests:
		return CodeRateLimited
	case status == fiber.StatusServiceUnavailable:
		return CodeUnavailable
	case status >= 400 && status < 500:
		return CodeBadRequest
	default:
		return CodeInternal
	}
}

func NewErrorHandler(logger *slog.Logger) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		status := StatusFor(err)
		body := errorResponse{
			Code:      CodeFor(err),
			Message:   clientMessage(err),
			RequestID: c.GetRespHeader(fiber.HeaderXRequestID),
			Fields:    fieldErrors(err),
		}

		switch status {
		case fiber.StatusInternalServerError:
			logger.LogAttrs(c.UserContext(), slog.LevelError, "unhandled request error",
				slog.String("method", c.Method()),
				slog.String("path", c.Path()),
				slog.String("error", err.Error()),
			)
			body.Message = "internal server error"
			body.Fields = nil

		case fiber.StatusServiceUnavailable:
			logger.LogAttrs(c.UserContext(), slog.LevelWarn, "request did not complete in time",
				slog.String("method", c.Method()),
				slog.String("path", c.Path()),
				slog.String("error", err.Error()),
			)
			body.Message = "the server could not complete the request in time; try again"
			body.Fields = nil
			c.Set(fiber.HeaderRetryAfter, retryAfterUnavailable)
		}

		return JSON(c, status, body)
	}
}

func clientMessage(err error) string {
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return fiberErr.Message
	}

	var validationErrs validator.ValidationErrors
	if errors.As(err, &validationErrs) {
		return "request body is invalid"
	}

	// A wrapped sentinel reads "validation failed: title must not be empty";
	// the sentinel is already the code, so only the detail is worth sending.
	message := err.Error()
	for _, sentinel := range []error{
		apperr.ErrValidation, apperr.ErrNotFound, apperr.ErrConflict,
		apperr.ErrUnauthorized, apperr.ErrForbidden, apperr.ErrInvalidCredentials,
	} {
		prefix := sentinel.Error() + ": "
		if errors.Is(err, sentinel) && strings.HasPrefix(message, prefix) {
			return strings.TrimPrefix(message, prefix)
		}
	}

	return message
}

// fieldErrors says which fields were rejected, so a client can point at the
// input instead of parsing the message.
func fieldErrors(err error) []fieldError {
	var validationErrs validator.ValidationErrors
	if !errors.As(err, &validationErrs) {
		return nil
	}

	fields := make([]fieldError, 0, len(validationErrs))
	for _, fe := range validationErrs {
		fields = append(fields, fieldError{
			Field:   fe.Field(),
			Message: fmt.Sprintf("failed '%s' validation", fe.Tag()),
		})
	}
	return fields
}
