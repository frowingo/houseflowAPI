package handlers

import "github.com/gofiber/fiber/v2"

// HealthCheck godoc
// @Summary Health check
// @Description Check if the API is running
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /health [get]
func HealthCheck(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":  "ok",
		"message": "HouseFlow API is running",
	})
}
