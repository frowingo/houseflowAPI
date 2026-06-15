package main

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/middleware"
	"houseflowApi/internal/services"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
)

func SetupRoutes(app *fiber.App, client *mongo.Client, dbName string) {

	api := app.Group("/api/v1", middleware.IPRateLimit())

	baseRoutes := api.Group("/base")
	baseRoutes.Get("/health", controllers.HealthController)

	// - AUTH -
	notificationService := services.NewNotificationService()
	authService := services.NewAuthService(abstract.New[entities.User](client, dbName), notificationService)
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
		abstract.New[entities.User](client, dbName),
		abstract.New[entities.House](client, dbName),
		abstract.New[entities.ImageAsset](client, dbName),
	)
	userController := controllers.NewUserController(userService)

	userRoutes := api.Group("/user", middleware.AuthRequired(), middleware.UserRateLimit())
	userRoutes.Post("", middleware.RequireRole(int(entities.SuperAdmin)), userController.NewUser)
	userRoutes.Get("/usersList", middleware.RequireRole(int(entities.SuperAdmin)), userController.ListUsers)
	userRoutes.Get("/getByEmail", userController.GetUserByEmail)
	userRoutes.Get("/getUsersByHouse", userController.GetUsersByHouse)
	userRoutes.Get("/getImages", userController.GetImages)
	userRoutes.Get("/getImage", userController.GetImage)
	userRoutes.Post("/images", middleware.RequireRole(int(entities.SuperAdmin)), userController.CreateImageAsset)
	userRoutes.Put("/images", middleware.RequireRole(int(entities.SuperAdmin)), userController.UpdateImageAsset)
	userRoutes.Put("/profile/:id", userController.UpdateProfile)
	userRoutes.Delete("/:id", middleware.RequireRole(int(entities.SuperAdmin)), userController.DeleteUser)
	// ----------

	// - HOUSE -
	houseService := services.NewHouseService(
		abstract.New[entities.House](client, dbName),
		abstract.New[entities.User](client, dbName),
		abstract.New[entities.Chore](client, dbName),
		client,
		dbName,
	)
	houseController := controllers.NewHouseController(houseService)

	houseRoutes := api.Group("/house", middleware.AuthRequired(), middleware.UserRateLimit())
	houseRoutes.Get("/details", houseController.GetHouseDetails)
	houseRoutes.Post("/create", houseController.CreateHouse)
	houseRoutes.Post("/join", houseController.JoinHouseByCode)
	// ----------

	// - CHORE -
	choreService := services.NewChoreService(
		abstract.New[entities.Chore](client, dbName),
		abstract.New[entities.House](client, dbName),
		abstract.New[entities.User](client, dbName),
		client,
		dbName,
	)
	choreController := controllers.NewChoreController(choreService)

	choreRoutes := api.Group("/chore", middleware.AuthRequired(), middleware.UserRateLimit())
	choreRoutes.Post("", choreController.CreateChore)
	choreRoutes.Put("/status", choreController.UpdateChoreStatus)
	choreRoutes.Put("/:id", choreController.UpdateChore)
	// ----------
}
