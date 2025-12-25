package main

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/handlers"
	"houseflowApi/internal/services"

	"github.com/gofiber/fiber/v2"
)

func SetupRoutes(app *fiber.App) {

	api := app.Group("/api/v1")

	baseRoutes := api.Group("/base")
	baseRoutes.Get("/health", handlers.HealthCheck)

	userService := services.NewUserService(&abstract.DbRepository[entities.User]{})
	userHandler := handlers.NewUserHandler(userService)

	userRoutes := api.Group("/user")
	userRoutes.Post("", userHandler.NewUser)
	userRoutes.Get("/usersList", userHandler.ListUsers)
	userRoutes.Get("/getByEmail", userHandler.GetUserByEmail)
	userRoutes.Delete("/:id", userHandler.DeleteUser)
}
