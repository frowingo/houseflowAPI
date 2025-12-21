package main

import (
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/swagger"

	_ "houseflowApi/docs" // Swagger docs
)

// @title HouseFlow API
// @version 1.0
// @description HouseFlow API Documentation
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.email support@houseflow.com

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @host localhost:3162
// @BasePath /api/v1
// @schemes http https
func main() {
	app := fiber.New(fiber.Config{
		AppName: "HouseFlow API",
	})

	app.Use(logger.New())
	app.Use(cors.New())

	app.Get("/swagger/*", swagger.HandlerDefault)

	api := app.Group("/api/v1")

	api.Get("/health", healthCheck)

	log.Fatal(app.Listen(":3162"))
}

// HealthCheck godoc
// @Summary Health check
// @Description Check if the API is running
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /health [get]
func healthCheck(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":  "ok",
		"message": "HouseFlow API is running",
	})
}
