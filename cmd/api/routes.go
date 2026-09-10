package main

import (
	"context"
	"houseflowApi/internal/abstract"
	choreCommands "houseflowApi/internal/application/chore/commands"
	chorePolicies "houseflowApi/internal/application/chore/policies"
	housecommands "houseflowApi/internal/application/house/commands"
	housePolicies "houseflowApi/internal/application/house/policies"
	housequeries "houseflowApi/internal/application/house/queries"
	imageAssetCommands "houseflowApi/internal/application/imageAsset/commands"
	imageAssetQueries "houseflowApi/internal/application/imageAsset/queries"
	userCommands "houseflowApi/internal/application/user/commands"
	userQueries "houseflowApi/internal/application/user/queries"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/middleware"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/internal/services"
	"log"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
)

func SetupRoutes(ctx context.Context, app *fiber.App, client *mongo.Client, dbName string) {
	applicationMediator := cqrs.New()

	localizationService := services.NewLocalizationService(
		abstract.New[entities.Localization](client, dbName),
		abstract.New[entities.LocalizationLanguageOption](client, dbName),
	)
	if err := localizationService.LoadCache(ctx); err != nil {
		log.Println("localization cache warmup failed:", err)
	}
	localizationController := controllers.NewLocalizationController(localizationService)

	api := app.Group("/api/v1", middleware.IPRateLimit(localizationService))

	baseRoutes := api.Group("/base")
	baseRoutes.Get("/health", controllers.LocalizedHealthController(localizationService))

	// - LOCALIZATION -
	localizationRoutes := api.Group("/localization", middleware.IPRateLimit(localizationService))
	localizationRoutes.Get("/languages", localizationController.GetLanguages)
	localizationRoutes.Get("/language/:prefix", middleware.AuthRequired(localizationService), localizationController.GetLanguage)
	localizationRoutes.Post("/language", middleware.AuthRequired(localizationService), middleware.RequireRoleWithLocalizer(localizationService, int(entities.SuperAdmin)), localizationController.InsertLocalizationLanguage)
	localizationRoutes.Get("/plaintext/:language", localizationController.GetPlaintexts)

	languageRoutes := api.Group("/language", middleware.AuthRequired(localizationService), middleware.UserRateLimit(localizationService))
	languageRoutes.Post("", middleware.RequireRoleWithLocalizer(localizationService, int(entities.SuperAdmin)), localizationController.InsertLocalizations)
	languageRoutes.Post("/", middleware.RequireRoleWithLocalizer(localizationService, int(entities.SuperAdmin)), localizationController.InsertLocalizations)
	// ----------

	// - AUTH -
	notificationService := services.NewNotificationService()
	authService := services.NewAuthService(
		abstract.New[entities.User](client, dbName),
		notificationService,
		abstract.New[entities.UserInfoHistory](client, dbName),
	)
	authController := controllers.NewAuthController(authService, localizationService)

	authRoutes := api.Group("/auth", middleware.StrictRateLimit(localizationService))
	authRoutes.Get("/isAuth", authController.IsAuth)
	authRoutes.Post("/login", authController.Login)
	authRoutes.Post("/signup", authController.Signup)
	authRoutes.Post("/forget", authController.ForgotPassword)
	authRoutes.Post("/reset", authController.ResetPassword)
	authRoutes.Get("/validate-email", middleware.AuthRequired(localizationService), authController.SendEmailVerificationCode)
	authRoutes.Post("/validate-email", middleware.AuthRequired(localizationService), authController.ValidateEmail)
	// ----------

	// - USER -
	userRepository := abstract.New[entities.User](client, dbName)
	houseRepository := abstract.New[entities.House](client, dbName)
	imageAssetRepository := abstract.New[entities.ImageAsset](client, dbName)
	userInfoHistoryRepository := abstract.New[entities.UserInfoHistory](client, dbName)
	houseMembershipPolicy := housePolicies.NewMembershipPolicy(houseRepository)
	imageCache := helpers.NewInMemoryCache[[]dtos.ImageAssetResultModel]()

	createUserHandler := userCommands.NewCreateUserHandler(userRepository, userInfoHistoryRepository)
	deleteUserHandler := userCommands.NewDeleteUserHandler(userRepository)
	updateProfileHandler := userCommands.NewUpdateProfileHandler(userRepository, userInfoHistoryRepository)
	getUserByEmailHandler := userQueries.NewGetUserByEmailHandler(userRepository)
	listUsersHandler := userQueries.NewListUsersHandler(userRepository)
	getUsersByHouseHandler := userQueries.NewGetUsersByHouseHandler(userRepository, houseMembershipPolicy)
	createImageAssetHandler := imageAssetCommands.NewCreateImageAssetHandler(imageAssetRepository, imageCache)
	updateImageAssetHandler := imageAssetCommands.NewUpdateImageAssetHandler(imageAssetRepository, imageCache)
	getImagesByCategoryHandler := imageAssetQueries.NewGetImagesByCategoryHandler(imageAssetRepository, imageCache)
	getImageByPublicIDHandler := imageAssetQueries.NewGetImageByPublicIDHandler(imageAssetRepository)

	cqrs.MustRegister[*dtos.NewUserModel, userCommands.CreateUserCommand](applicationMediator, createUserHandler)
	cqrs.MustRegister[cqrs.NoResult, userCommands.DeleteUserCommand](applicationMediator, deleteUserHandler)
	cqrs.MustRegister[*dtos.UserResultModel, userCommands.UpdateProfileCommand](applicationMediator, updateProfileHandler)
	cqrs.MustRegister[*dtos.UserResultModel, userQueries.GetUserByEmailQuery](applicationMediator, getUserByEmailHandler)
	cqrs.MustRegister[[]dtos.UserResultModel, userQueries.ListUsersQuery](applicationMediator, listUsersHandler)
	cqrs.MustRegister[[]dtos.UserResultModel, userQueries.GetUsersByHouseQuery](applicationMediator, getUsersByHouseHandler)
	cqrs.MustRegister[cqrs.NoResult, imageAssetCommands.CreateImageAssetCommand](applicationMediator, createImageAssetHandler)
	cqrs.MustRegister[cqrs.NoResult, imageAssetCommands.UpdateImageAssetCommand](applicationMediator, updateImageAssetHandler)
	cqrs.MustRegister[[]dtos.ImageAssetResultModel, imageAssetQueries.GetImagesByCategoryQuery](applicationMediator, getImagesByCategoryHandler)
	cqrs.MustRegister[*dtos.ImageAssetResultModel, imageAssetQueries.GetImageByPublicIDQuery](applicationMediator, getImageByPublicIDHandler)
	userController := controllers.NewUserController(applicationMediator, localizationService)

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
	houseChoreRepository := abstract.New[entities.Chore](client, dbName)
	houseChoreStatusHistoryRepository := abstract.New[entities.ChoreStatusHistory](client, dbName)
	houseChoreReviewVoteRepository := abstract.New[entities.ChoreReviewVote](client, dbName)
	houseAnnouncementRepository := abstract.New[entities.Announcement](client, dbName)

	createHouseHandler := housecommands.NewCreateHouseHandler(houseRepository, userRepository)
	joinHouseHandler := housecommands.NewJoinHouseHandler(houseRepository, userRepository)
	createAnnouncementHandler := housecommands.NewCreateAnnouncementHandler(
		houseMembershipPolicy,
		userRepository,
		houseAnnouncementRepository,
	)
	getHouseDetailsHandler := housequeries.NewGetHouseDetailsHandler(
		houseMembershipPolicy,
		houseRepository,
		userRepository,
		houseChoreRepository,
		houseChoreStatusHistoryRepository,
		houseChoreReviewVoteRepository,
		houseAnnouncementRepository,
	)
	cqrs.MustRegister[*entities.House, housecommands.CreateHouseCommand](applicationMediator, createHouseHandler)
	cqrs.MustRegister[*entities.House, housecommands.JoinHouseCommand](applicationMediator, joinHouseHandler)
	cqrs.MustRegister[*dtos.AnnouncementResponseModel, housecommands.CreateAnnouncementCommand](applicationMediator, createAnnouncementHandler)
	cqrs.MustRegister[*dtos.HouseDetailsModel, housequeries.GetHouseDetailsQuery](applicationMediator, getHouseDetailsHandler)
	houseController := controllers.NewHouseController(
		applicationMediator,
		localizationService,
	)

	houseRoutes := api.Group("/house", middleware.AuthRequired(localizationService), middleware.UserRateLimit(localizationService))
	houseRoutes.Get("/details", houseController.GetHouseDetails)
	houseRoutes.Post("/announcement", houseController.CreateAnnouncement)
	houseRoutes.Post("/create", houseController.CreateHouse)
	houseRoutes.Post("/join", houseController.JoinHouseByCode)
	// ----------

	// - CHORE -
	choreAssignmentPolicy := chorePolicies.NewAssignmentPolicy(userRepository)
	choreWorkflowPolicy := chorePolicies.NewWorkflowPolicy()
	createChoreHandler := choreCommands.NewCreateChoreHandler(
		houseChoreRepository,
		houseChoreStatusHistoryRepository,
		houseChoreReviewVoteRepository,
		houseMembershipPolicy,
		choreAssignmentPolicy,
	)
	updateChoreHandler := choreCommands.NewUpdateChoreHandler(
		houseChoreRepository,
		houseChoreStatusHistoryRepository,
		houseChoreReviewVoteRepository,
		houseMembershipPolicy,
		choreAssignmentPolicy,
	)
	updateChoreStatusHandler := choreCommands.NewUpdateChoreStatusHandler(
		houseChoreRepository,
		houseChoreStatusHistoryRepository,
		houseChoreReviewVoteRepository,
		houseMembershipPolicy,
		choreWorkflowPolicy,
	)
	reviewChoreHandler := choreCommands.NewReviewChoreHandler(
		houseChoreRepository,
		houseChoreStatusHistoryRepository,
		houseChoreReviewVoteRepository,
		houseMembershipPolicy,
		choreWorkflowPolicy,
	)
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.CreateChoreCommand](applicationMediator, createChoreHandler)
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.UpdateChoreCommand](applicationMediator, updateChoreHandler)
	cqrs.MustRegister[[]dtos.ChoreResponseModel, choreCommands.UpdateChoreStatusCommand](applicationMediator, updateChoreStatusHandler)
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.ReviewChoreCommand](applicationMediator, reviewChoreHandler)
	choreController := controllers.NewChoreController(applicationMediator, localizationService)

	choreRoutes := api.Group("/chore", middleware.AuthRequired(localizationService), middleware.UserRateLimit(localizationService))
	choreRoutes.Post("", choreController.CreateChore)
	choreRoutes.Put("/status", choreController.UpdateChoreStatus)
	choreRoutes.Put("/review", choreController.ReviewChore)
	choreRoutes.Put("/:id", choreController.UpdateChore)
	// ----------
}
