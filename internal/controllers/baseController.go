package controllers

import (
	"houseflowApi/internal/helpers"

	"github.com/gofiber/fiber/v2"
)

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

func LocalizedHealthController(localizer helpers.MessageLocalizer) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":  "ok",
			"message": helpers.LocalizedMessage(c, localizer, "base.message.api_running"),
		})
	}
}
