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
	authRoutes.Post("/login", authController.Login)
	authRoutes.Post("/signup", authController.Signup)

	userService := services.NewUserService(&abstract.DbRepository[entities.User]{})
	userController := controllers.NewUserController(userService)

	userRoutes := api.Group("/user", middleware.AuthRequired())
	userRoutes.Post("", userController.NewUser)
	userRoutes.Get("/usersList", userController.ListUsers)
	userRoutes.Get("/getByEmail", userController.GetUserByEmail)
	userRoutes.Delete("/:id", userController.DeleteUser)

	houseService := services.NewHouseService(
		&abstract.DbRepository[entities.House]{},
		&abstract.DbRepository[entities.User]{},
	)
	houseController := controllers.NewHouseController(houseService)

	houseRoutes := api.Group("/house", middleware.AuthRequired())
	houseRoutes.Post("/create", houseController.CreateHouse)
	houseRoutes.Post("/join", houseController.JoinHouseByCode)

	choreService := services.NewChoreService(&abstract.DbRepository[entities.Chore]{})
	choreController := controllers.NewChoreController(choreService)

	choreRoutes := api.Group("/chore", middleware.AuthRequired())
	choreRoutes.Post("", choreController.CreateChore)
	choreRoutes.Put("/status", choreController.UpdateChoreStatus)
	choreRoutes.Put("/:id", choreController.UpdateChore)
}
