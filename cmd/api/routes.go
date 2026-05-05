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

	api := app.Group("/api/v1", middleware.IPRateLimit())

	baseRoutes := api.Group("/base")
	baseRoutes.Get("/health", controllers.HealthController)

	// - AUTH -
	authService := services.NewAuthService(&abstract.DbRepository[entities.User]{})
	authController := controllers.NewAuthController(authService)

	authRoutes := api.Group("/auth", middleware.StrictRateLimit())
	authRoutes.Get("/isAuth", authController.IsAuth)
	authRoutes.Post("/login", authController.Login)
	authRoutes.Post("/signup", authController.Signup)
	authRoutes.Post("/forget", authController.ForgotPassword)
	authRoutes.Post("/reset", authController.ResetPassword)
	// ----------

	// - USER -
	userService := services.NewUserService(
		&abstract.DbRepository[entities.User]{},
		&abstract.DbRepository[entities.House]{},
	)
	userController := controllers.NewUserController(userService)

	userRoutes := api.Group("/user", middleware.AuthRequired(), middleware.UserRateLimit())
	userRoutes.Post("", userController.NewUser, middleware.RequireRole(int(entities.SuperAdmin)))
	userRoutes.Get("/usersList", middleware.RequireRole(int(entities.SuperAdmin)), userController.ListUsers)
	userRoutes.Get("/getByEmail", userController.GetUserByEmail)
	userRoutes.Get("/getUsersByHouse", userController.GetUsersByHouse)
	userRoutes.Put("/profile/:id", userController.UpdateProfile)
	userRoutes.Delete("/:id", middleware.RequireRole(int(entities.SuperAdmin)), userController.DeleteUser)
	// ----------

	// - HOUSE -
	houseService := services.NewHouseService(
		&abstract.DbRepository[entities.House]{},
		&abstract.DbRepository[entities.User]{},
		&abstract.DbRepository[entities.Chore]{},
	)
	houseController := controllers.NewHouseController(houseService)

	houseRoutes := api.Group("/house", middleware.AuthRequired(), middleware.UserRateLimit())
	houseRoutes.Get("/details", houseController.GetHouseDetails)
	houseRoutes.Post("/create", houseController.CreateHouse)
	houseRoutes.Post("/join", houseController.JoinHouseByCode)
	// ----------

	// - CHORE -
	choreService := services.NewChoreService(&abstract.DbRepository[entities.Chore]{})
	choreController := controllers.NewChoreController(choreService)

	choreRoutes := api.Group("/chore", middleware.AuthRequired(), middleware.UserRateLimit())
	choreRoutes.Post("", choreController.CreateChore)
	choreRoutes.Put("/status", choreController.UpdateChoreStatus)
	choreRoutes.Put("/:id", choreController.UpdateChore)
	// ----------
}
