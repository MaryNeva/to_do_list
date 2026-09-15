package http

import "github.com/gofiber/fiber/v2"

func JSON(c *fiber.Ctx, status int, payload any) error {
	return c.Status(status).JSON(payload)
}

// errorResponse is the JSON body of every error response.
type errorResponse struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}
