package main

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/middleware"
	"houseflowApi/internal/services"
	"log"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
)

func SetupRoutes(app *fiber.App, client *mongo.Client, dbName string) {

	localizationService := services.NewLocalizationService(
		abstract.New[entities.Localization](client, dbName),
		abstract.New[entities.LocalizationLanguageOption](client, dbName),
	)
	if err := localizationService.LoadCache(); err != nil {
		log.Println("localization cache warmup failed:", err)
	}
	localizationController := controllers.NewLocalizationController(localizationService)

	api := app.Group("/api/v1", middleware.IPRateLimit(localizationService))

	baseRoutes := api.Group("/base")
	baseRoutes.Get("/health", controllers.LocalizedHealthController(localizationService))

	// - LOCALIZATION -
	localizationRoutes := api.Group("/localization", middleware.UserRateLimit(localizationService))
	localizationRoutes.Get("/languages", middleware.AuthRequired(localizationService), localizationController.GetLanguages)
	localizationRoutes.Get("/language/:prefix", middleware.AuthRequired(localizationService), localizationController.GetLanguage)
	localizationRoutes.Post("/language", middleware.AuthRequired(localizationService), middleware.RequireRoleWithLocalizer(localizationService, int(entities.SuperAdmin)), localizationController.InsertLocalizationLanguage)
	localizationRoutes.Get("/plaintext/:language", localizationController.GetPlaintexts)

	languageRoutes := api.Group("/language", middleware.AuthRequired(localizationService), middleware.UserRateLimit(localizationService))
	languageRoutes.Post("", middleware.RequireRoleWithLocalizer(localizationService, int(entities.SuperAdmin)), localizationController.InsertLocalizations)
	languageRoutes.Post("/", middleware.RequireRoleWithLocalizer(localizationService, int(entities.SuperAdmin)), localizationController.InsertLocalizations)
	// ----------

	// - AUTH -
	notificationService := services.NewNotificationService()
	authService := services.NewAuthService(abstract.New[entities.User](client, dbName), notificationService)
	authController := controllers.NewAuthController(authService, localizationService)

	authRoutes := api.Group("/auth", middleware.StrictRateLimit(localizationService))
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
	userController := controllers.NewUserController(userService, localizationService)

	userRoutes := api.Group("/user", middleware.AuthRequired(localizationService), middleware.UserRateLimit(localizationService))
	userRoutes.Post("", middleware.RequireRoleWithLocalizer(localizationService, int(entities.SuperAdmin)), userController.NewUser)
	userRoutes.Get("/usersList", middleware.RequireRoleWithLocalizer(localizationService, int(entities.SuperAdmin)), userController.ListUsers)
	userRoutes.Get("/getByEmail", userController.GetUserByEmail)
	userRoutes.Get("/getUsersByHouse", userController.GetUsersByHouse)
	userRoutes.Get("/getImages", userController.GetImages)
	userRoutes.Get("/getImage", userController.GetImage)
	userRoutes.Post("/images", middleware.RequireRoleWithLocalizer(localizationService, int(entities.SuperAdmin)), userController.CreateImageAsset)
	userRoutes.Put("/images", middleware.RequireRoleWithLocalizer(localizationService, int(entities.SuperAdmin)), userController.UpdateImageAsset)
	userRoutes.Put("/profile/:id", userController.UpdateProfile)
	userRoutes.Delete("/:id", middleware.RequireRoleWithLocalizer(localizationService, int(entities.SuperAdmin)), userController.DeleteUser)
	// ----------

	// - HOUSE -
	houseService := services.NewHouseService(
		abstract.New[entities.House](client, dbName),
		abstract.New[entities.User](client, dbName),
		abstract.New[entities.Chore](client, dbName),
		client,
		dbName,
	)
	houseController := controllers.NewHouseController(houseService, localizationService)

	houseRoutes := api.Group("/house", middleware.AuthRequired(localizationService), middleware.UserRateLimit(localizationService))
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
	choreController := controllers.NewChoreController(choreService, localizationService)

	choreRoutes := api.Group("/chore", middleware.AuthRequired(localizationService), middleware.UserRateLimit(localizationService))
	choreRoutes.Post("", choreController.CreateChore)
	choreRoutes.Put("/status", choreController.UpdateChoreStatus)
	choreRoutes.Put("/review", choreController.ReviewChore)
	choreRoutes.Put("/:id", choreController.UpdateChore)
	// ----------
}
