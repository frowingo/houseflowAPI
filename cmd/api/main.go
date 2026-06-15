package main

import (
	"context"
	"log"
	"os"

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
		AppName:     "HouseFlow API",
		ProxyHeader: fiber.HeaderXForwardedFor,
	})

	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: getAllowedOrigins(),
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET, POST, PUT, DELETE, OPTIONS",
	}))

	app.Get("/swagger/*", swagger.HandlerDefault)

	SetupRoutes(app, mongoClient, db.Name())

	log.Fatal(app.Listen(":3162"))
}

func getAllowedOrigins() string {
	if origins := os.Getenv("CORS_ALLOW_ORIGINS"); origins != "" {
		return origins
	}
	if os.Getenv("APP_ENV") == "production" {
		log.Println("CORS_ALLOW_ORIGINS is empty in production; using Fiber's default origin policy")
		return ""
	}
	return "*"
}
