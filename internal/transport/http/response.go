package http

import "github.com/gofiber/fiber/v2"

func JSON(c *fiber.Ctx, status int, payload any) error {
	return c.Status(status).JSON(payload)
}

// errorResponse is the JSON body of every error. Clients should branch on
// Code; Message is human-readable and may change.
type errorResponse struct {
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	RequestID string       `json:"request_id,omitempty"`
	Fields    []fieldError `json:"fields,omitempty"`
}

type fieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}
