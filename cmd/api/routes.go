package main

import (
	"houseflowApi/internal/handlers"

	"github.com/gofiber/fiber/v2"
)

func SetupRoutes(app *fiber.App) {

	api := app.Group("/api/v1")

	baseRoutes := api.Group("/base")
	baseRoutes.Get("/health", handlers.HealthCheck)
}
