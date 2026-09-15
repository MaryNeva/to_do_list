package handler

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
)

func parseIDParam(c *fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, "invalid id parameter")
	}
	return id, nil
}
