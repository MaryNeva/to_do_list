package handler

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/domain"
)

func parseIDParam(c *fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, "invalid id parameter")
	}
	return id, nil
}

func parsePageParams(c *fiber.Ctx) (domain.PageRequest, error) {
	page := domain.PageRequest{}

	if raw := c.Query("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			return domain.PageRequest{}, fiber.NewError(fiber.StatusBadRequest, "limit must be a positive whole number")
		}
		page.Limit = limit
	}

	if raw := c.Query("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return domain.PageRequest{}, fiber.NewError(fiber.StatusBadRequest, "offset must be zero or a positive whole number")
		}
		page.Offset = offset
	}

	return page, nil
}

func parseTaskFilter(c *fiber.Ctx) (domain.TaskFilter, error) {
	page, err := parsePageParams(c)
	if err != nil {
		return domain.TaskFilter{}, err
	}

	filter := domain.TaskFilter{Page: page, Sort: domain.SortByCreatedAt, Order: domain.OrderDesc}

	if raw := c.Query("status"); raw != "" {
		status := domain.TaskStatus(raw)
		if !domain.IsValidStatus(status) {
			return domain.TaskFilter{}, fiber.NewError(fiber.StatusBadRequest,
				"status must be one of: created, in_progress, completed")
		}
		filter.Status = &status
	}

	if raw := c.Query("sort"); raw != "" {
		switch domain.TaskSortField(raw) {
		case domain.SortByCreatedAt, domain.SortByUpdatedAt, domain.SortByTitle, domain.SortByStatus:
			filter.Sort = domain.TaskSortField(raw)
		default:
			return domain.TaskFilter{}, fiber.NewError(fiber.StatusBadRequest,
				"sort must be one of: created_at, updated_at, title, status")
		}
	}

	if raw := c.Query("order"); raw != "" {
		switch domain.SortOrder(raw) {
		case domain.OrderAsc, domain.OrderDesc:
			filter.Order = domain.SortOrder(raw)
		default:
			return domain.TaskFilter{}, fiber.NewError(fiber.StatusBadRequest, "order must be asc or desc")
		}
	}

	return filter, nil
}
