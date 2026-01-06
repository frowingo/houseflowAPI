package main

import (
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/swagger"

	_ "houseflowApi/external/swagger/docs" // Swagger docs
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

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.
func main() {
	app := fiber.New(fiber.Config{
		AppName: "HouseFlow API",
	})

	app.Use(logger.New())
	app.Use(cors.New())

	app.Get("/swagger/*", swagger.HandlerDefault)

	SetupRoutes(app)

	log.Fatal(app.Listen(":3162"))
}
