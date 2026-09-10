package main

import (
	"context"
	authCommands "houseflowApi/internal/application/auth/commands"
	authQueries "houseflowApi/internal/application/auth/queries"
	choreCommands "houseflowApi/internal/application/chore/commands"
	chorePolicies "houseflowApi/internal/application/chore/policies"
	housecommands "houseflowApi/internal/application/house/commands"
	housePolicies "houseflowApi/internal/application/house/policies"
	housequeries "houseflowApi/internal/application/house/queries"
	imageAssetCommands "houseflowApi/internal/application/imageAsset/commands"
	imageAssetQueries "houseflowApi/internal/application/imageAsset/queries"
	localizationCommands "houseflowApi/internal/application/localization/commands"
	localizationQueries "houseflowApi/internal/application/localization/queries"
	userCommands "houseflowApi/internal/application/user/commands"
	userQueries "houseflowApi/internal/application/user/queries"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	infrastructureLocalization "houseflowApi/internal/infrastructure/localization"
	"houseflowApi/internal/infrastructure/middleware"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/internal/services"
	"log"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
)

func SetupRoutes(ctx context.Context, app *fiber.App, client *mongo.Client, dbName string) {
	applicationMediator := cqrs.New()

	localizationRepository := database.NewDbContext[entities.Localization](client, dbName)
	languageRepository := database.NewDbContext[entities.LocalizationLanguageOption](client, dbName)
	localizationCache := infrastructureLocalization.NewCache(localizationRepository)
	if err := localizationCache.Load(ctx); err != nil {
		log.Println("localization cache warmup failed:", err)
	}
	getPlaintextsHandler := localizationQueries.NewGetPlaintextsHandler(localizationRepository, localizationCache)
	getLanguagesHandler := localizationQueries.NewGetLanguagesHandler(languageRepository)
	getLanguageHandler := localizationQueries.NewGetLanguageHandler(languageRepository)
	insertLocalizationsHandler := localizationCommands.NewInsertLocalizationsHandler(localizationRepository, localizationCache)
	insertLocalizationLanguageHandler := localizationCommands.NewInsertLocalizationLanguageHandler(languageRepository)
	cqrs.MustRegister[[]dtos.LocalizationPlaintextResponseModel, localizationQueries.GetPlaintextsQuery](applicationMediator, getPlaintextsHandler)
	cqrs.MustRegister[[]dtos.LocalizationLanguageResponseModel, localizationQueries.GetLanguagesQuery](applicationMediator, getLanguagesHandler)
	cqrs.MustRegister[[]dtos.LocalizationLanguageResponseModel, localizationQueries.GetLanguageQuery](applicationMediator, getLanguageHandler)
	cqrs.MustRegister[cqrs.NoResult, localizationCommands.InsertLocalizationsCommand](applicationMediator, insertLocalizationsHandler)
	cqrs.MustRegister[cqrs.NoResult, localizationCommands.InsertLocalizationLanguageCommand](applicationMediator, insertLocalizationLanguageHandler)
	localizationController := controllers.NewLocalizationController(applicationMediator, localizationCache)

	api := app.Group("/api/v1", middleware.IPRateLimit(localizationCache))

	baseRoutes := api.Group("/base")
	baseRoutes.Get("/health", controllers.LocalizedHealthController(localizationCache))

	// - LOCALIZATION -
	localizationRoutes := api.Group("/localization", middleware.IPRateLimit(localizationCache))
	localizationRoutes.Get("/languages", localizationController.GetLanguages)
	localizationRoutes.Get("/language/:prefix", middleware.AuthRequired(localizationCache), localizationController.GetLanguage)
	localizationRoutes.Post("/language", middleware.AuthRequired(localizationCache), middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), localizationController.InsertLocalizationLanguage)
	localizationRoutes.Get("/plaintext/:language", localizationController.GetPlaintexts)

	languageRoutes := api.Group("/language", middleware.AuthRequired(localizationCache), middleware.UserRateLimit(localizationCache))
	languageRoutes.Post("", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), localizationController.InsertLocalizations)
	languageRoutes.Post("/", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), localizationController.InsertLocalizations)
	// ----------

	// - AUTH -
	userRepository := database.NewDbContext[entities.User](client, dbName)
	userInfoHistoryRepository := database.NewDbContext[entities.UserInfoHistory](client, dbName)
	notificationService := services.NewNotificationService()
	loginHandler := authCommands.NewLoginHandler(userRepository)
	signUpHandler := authCommands.NewSignUpHandler(userRepository, userInfoHistoryRepository)
	forgotPasswordHandler := authCommands.NewForgotPasswordHandler(userRepository, notificationService)
	resetPasswordHandler := authCommands.NewResetPasswordHandler(userRepository)
	sendEmailVerificationCodeHandler := authCommands.NewSendEmailVerificationCodeHandler(userRepository, notificationService)
	validateEmailHandler := authCommands.NewValidateEmailHandler(userRepository)
	validateAuthHandler := authQueries.NewValidateAuthHandler(userRepository)
	cqrs.MustRegister[string, authCommands.LoginCommand](applicationMediator, loginHandler)
	cqrs.MustRegister[string, authCommands.SignUpCommand](applicationMediator, signUpHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.ForgotPasswordCommand](applicationMediator, forgotPasswordHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.ResetPasswordCommand](applicationMediator, resetPasswordHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.SendEmailVerificationCodeCommand](applicationMediator, sendEmailVerificationCodeHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.ValidateEmailCommand](applicationMediator, validateEmailHandler)
	cqrs.MustRegister[*dtos.UserResultModel, authQueries.ValidateAuthQuery](applicationMediator, validateAuthHandler)
	authController := controllers.NewAuthController(applicationMediator, localizationCache)

	authRoutes := api.Group("/auth", middleware.StrictRateLimit(localizationCache))
	authRoutes.Get("/isAuth", authController.IsAuth)
	authRoutes.Post("/login", authController.Login)
	authRoutes.Post("/signup", authController.Signup)
	authRoutes.Post("/forget", authController.ForgotPassword)
	authRoutes.Post("/reset", authController.ResetPassword)
	authRoutes.Get("/validate-email", middleware.AuthRequired(localizationCache), authController.SendEmailVerificationCode)
	authRoutes.Post("/validate-email", middleware.AuthRequired(localizationCache), authController.ValidateEmail)
	// ----------

	// - USER -
	houseRepository := database.NewDbContext[entities.House](client, dbName)
	imageAssetRepository := database.NewDbContext[entities.ImageAsset](client, dbName)
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
	userController := controllers.NewUserController(applicationMediator, localizationCache)

	userRoutes := api.Group("/user", middleware.AuthRequired(localizationCache), middleware.UserRateLimit(localizationCache))
	userRoutes.Post("", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), userController.NewUser)
	userRoutes.Get("/usersList", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), userController.ListUsers)
	userRoutes.Get("/getByEmail", userController.GetUserByEmail)
	userRoutes.Get("/getUsersByHouse", userController.GetUsersByHouse)
	userRoutes.Get("/getImages", userController.GetImages)
	userRoutes.Get("/getImage", userController.GetImage)
	userRoutes.Post("/images", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), userController.CreateImageAsset)
	userRoutes.Put("/images", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), userController.UpdateImageAsset)
	userRoutes.Put("/profile/:id", userController.UpdateProfile)
	userRoutes.Delete("/:id", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), userController.DeleteUser)
	// ----------

	// - HOUSE -
	houseChoreRepository := database.NewDbContext[entities.Chore](client, dbName)
	houseChoreStatusHistoryRepository := database.NewDbContext[entities.ChoreStatusHistory](client, dbName)
	houseChoreReviewVoteRepository := database.NewDbContext[entities.ChoreReviewVote](client, dbName)
	houseAnnouncementRepository := database.NewDbContext[entities.Announcement](client, dbName)

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
		localizationCache,
	)

	houseRoutes := api.Group("/house", middleware.AuthRequired(localizationCache), middleware.UserRateLimit(localizationCache))
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
	choreController := controllers.NewChoreController(applicationMediator, localizationCache)

	choreRoutes := api.Group("/chore", middleware.AuthRequired(localizationCache), middleware.UserRateLimit(localizationCache))
	choreRoutes.Post("", choreController.CreateChore)
	choreRoutes.Put("/status", choreController.UpdateChoreStatus)
	choreRoutes.Put("/review", choreController.ReviewChore)
	choreRoutes.Put("/:id", choreController.UpdateChore)
	// ----------
}
