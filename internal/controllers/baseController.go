package controllers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
)

const requestTimeout = 10 * time.Second
const readinessTimeout = 2 * time.Second

type ReadinessCheck func(context.Context) error

func requestContext(c *fiber.Ctx) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(c.UserContext(), requestTimeout)
	c.SetUserContext(ctx)
	return ctx, cancel
}

// HealthCheck godoc
// @Tags Base
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /base/health [get]
func HealthController(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":  "ok",
		"message": "HouseFlow API is running",
	})
}

func ReadinessController(check ReadinessCheck) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), readinessTimeout)
		defer cancel()

		if err := check(ctx); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"status": "unavailable",
			})
		}

		return c.JSON(fiber.Map{
			"status": "ready",
		})
	}
}
