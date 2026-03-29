package main

import (
	"context"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/swagger"

	"houseflowApi/external/migration"
	docs "houseflowApi/external/swagger/docs" // Swagger docs
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/migrations"
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
	ctx := context.Background()

	mongoClient, db, err := database.NewDatabase(ctx)
	if err != nil {
		log.Fatal("failed to connect to database:", err)
	}
	defer mongoClient.Disconnect(ctx)

	if err := migration.RunAll(ctx, db, migrations.AllMigrations()); err != nil {
		log.Fatal("migration failed:", err)
	}

	// Host'u boş bırakarak Swagger UI'nin isteğin geldiği host/scheme'i
	// kullanmasını sağla (localhost, OrbStack domain, vs. ile uyumlu).
	docs.SwaggerInfo.Host = ""
	docs.SwaggerInfo.Schemes = []string{}

	app := fiber.New(fiber.Config{
		AppName: "HouseFlow API",
	})

	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New())

	app.Get("/swagger/*", swagger.HandlerDefault)

	SetupRoutes(app)

	log.Fatal(app.Listen(":3162"))
}
