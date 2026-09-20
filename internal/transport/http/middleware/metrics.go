package middleware

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/apperr"
	httpapi "to-do-list/internal/transport/http"
)

type RequestRecorder interface {
	ObserveRequest(method, route string, status int, d time.Duration)
	IncInFlight()
	DecInFlight()
}

const (
	routeUnmatched = "unmatched"
	methodOther    = "other"
)

func Metrics(recorder RequestRecorder, skipPaths ...string) fiber.Handler {
	skip := make(map[string]struct{}, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = struct{}{}
	}

	return func(c *fiber.Ctx) error {
		if _, skipped := skip[c.Path()]; skipped {
			return c.Next()
		}

		start := time.Now()

		recorder.IncInFlight()
		defer recorder.DecInFlight()

		err := c.Next()

		status := c.Response().StatusCode()
		if err != nil {
			status = httpapi.StatusFor(err)
		}

		recorder.ObserveRequest(canonicalMethod(c), routeLabel(c, err), status, time.Since(start))

		return err
	}
}

func canonicalMethod(c *fiber.Ctx) string {
	switch c.Method() {
	case fiber.MethodGet:
		return fiber.MethodGet
	case fiber.MethodPost:
		return fiber.MethodPost
	case fiber.MethodPut:
		return fiber.MethodPut
	case fiber.MethodPatch:
		return fiber.MethodPatch
	case fiber.MethodDelete:
		return fiber.MethodDelete
	case fiber.MethodHead:
		return fiber.MethodHead
	case fiber.MethodOptions:
		return fiber.MethodOptions
	default:
		return methodOther
	}
}

func routeLabel(c *fiber.Ctx, err error) string {
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) &&
		fiberErr.Code == fiber.StatusNotFound &&
		!errors.Is(err, apperr.ErrNotFound) {
		return routeUnmatched
	}

	route := c.Route()
	if route == nil || route.Path == "" {
		return routeUnmatched
	}

	return route.Path
}
