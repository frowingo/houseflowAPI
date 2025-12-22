package handlers

import "github.com/gofiber/fiber/v2"

// HealthCheck godoc
// @Tags Base
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /base/health [get]
func HealthCheck(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":  "ok",
		"message": "HouseFlow API is running",
	})
}
