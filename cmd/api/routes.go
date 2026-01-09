package main

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/middleware"
	"houseflowApi/internal/services"

	"github.com/gofiber/fiber/v2"
)

func SetupRoutes(app *fiber.App) {

	api := app.Group("/api/v1")

	baseRoutes := api.Group("/base")
	baseRoutes.Get("/health", controllers.HealthController)

	authService := services.NewAuthService(&abstract.DbRepository[entities.User]{})
	authController := controllers.NewAuthController(authService)

	authRoutes := api.Group("/auth")
	authRoutes.Get("/login/:email/:password", authController.Login)
	authRoutes.Get("/signup/:email/:password", authController.Signup)

	userService := services.NewUserService(&abstract.DbRepository[entities.User]{})
	userController := controllers.NewUserController(userService)

	userRoutes := api.Group("/user", middleware.AuthRequired())
	userRoutes.Post("", userController.NewUser)
	userRoutes.Get("/usersList", userController.ListUsers)
	userRoutes.Get("/getByEmail", userController.GetUserByEmail)
	userRoutes.Delete("/:id", userController.DeleteUser)
}
