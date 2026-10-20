package main

import (
	"context"
	authCommands "houseflowApi/internal/application/auth/commands"
	authQueries "houseflowApi/internal/application/auth/queries"
	choreCommands "houseflowApi/internal/application/chore/commands"
	chorePolicies "houseflowApi/internal/application/chore/policies"
	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameApplication "houseflowApi/internal/application/game"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games"
	gameQueries "houseflowApi/internal/application/game/queries"
	houseApplication "houseflowApi/internal/application/house"
	housecommands "houseflowApi/internal/application/house/commands"
	housePolicies "houseflowApi/internal/application/house/policies"
	housequeries "houseflowApi/internal/application/house/queries"
	imageAssetCommands "houseflowApi/internal/application/imageAsset/commands"
	imageAssetQueries "houseflowApi/internal/application/imageAsset/queries"
	localizationCommands "houseflowApi/internal/application/localization/commands"
	localizationQueries "houseflowApi/internal/application/localization/queries"
	userCommands "houseflowApi/internal/application/user/commands"
	userQueries "houseflowApi/internal/application/user/queries"
	"houseflowApi/internal/config"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	infrastructureAppEvents "houseflowApi/internal/infrastructure/appEvents"
	"houseflowApi/internal/infrastructure/cqrs"
	infrastructureLocalization "houseflowApi/internal/infrastructure/localization"
	"houseflowApi/internal/infrastructure/middleware"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/internal/services"
	"log"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

func SetupRoutes(
	ctx context.Context,
	app *fiber.App,
	client *mongo.Client,
	dbName string,
	cfg config.ConfigInternal,
	socialAuthConfig config.ConfigSocialAuth,
	coordinator coordinationAbstract.Coordinator,
) (*realtime.Service, *infrastructureAppEvents.Service, error) {
	applicationMediator := cqrs.New()
	jwtService := helpers.NewJWTService(cfg.JWT.ApiSecret)

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

	baseRoutes := app.Group("/api/v1/base")
	baseRoutes.Get("/health", controllers.HealthController)
	baseRoutes.Get("/health/live", controllers.HealthController)
	baseRoutes.Get("/health/ready", controllers.ReadinessController(func(ctx context.Context) error {
		return client.Ping(ctx, readpref.Primary())
	}))

	api := app.Group("/api/v1", middleware.IPRateLimit(localizationCache))

	// - LOCALIZATION -
	localizationRoutes := api.Group("/localization", middleware.IPRateLimit(localizationCache))
	localizationRoutes.Get("/languages", localizationController.GetLanguages)
	localizationRoutes.Get("/language/:prefix", middleware.AuthRequired(jwtService, localizationCache), localizationController.GetLanguage)
	localizationRoutes.Post("/language", middleware.AuthRequired(jwtService, localizationCache), middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), localizationController.InsertLocalizationLanguage)
	localizationRoutes.Get("/plaintext/:language", localizationController.GetPlaintexts)

	languageRoutes := api.Group("/language", middleware.AuthRequired(jwtService, localizationCache), middleware.UserRateLimit(localizationCache))
	languageRoutes.Post("", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), localizationController.InsertLocalizations)
	languageRoutes.Post("/", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), localizationController.InsertLocalizations)
	// ----------

	// - AUTH -
	userRepository := database.NewDbContext[entities.User](client, dbName)
	houseRepository := database.NewDbContext[entities.House](client, dbName)
	appEventBus, _ := coordinator.(coordinationAbstract.AppEventBus)
	appEventService := infrastructureAppEvents.NewService(appEventBus, houseRepository, userRepository, webSocketAllowedOrigins())
	// The socket sends ready, house.changed and user.changed; clients refetch over HTTP.
	appEventRoutes := api.Group("/events", middleware.AuthRequired(jwtService, localizationCache))
	appEventRoutes.Get("", appEventService.UpgradeMiddleware(), appEventService.Handler())
	userInfoHistoryRepository := database.NewDbContext[entities.UserInfoHistory](client, dbName)
	notificationService := services.NewNotificationService(cfg.SMTP.Password)
	loginHandler := authCommands.NewLoginHandler(userRepository, jwtService)
	signUpHandler := authCommands.NewSignUpHandler(userRepository, userInfoHistoryRepository, jwtService)
	forgotPasswordHandler := authCommands.NewForgotPasswordHandler(
		userRepository, notificationService, cfg.PasswordReset.Secret, cfg.PasswordReset.ValidityMinutes,
	)
	resetPasswordHandler := authCommands.NewResetPasswordHandler(
		userRepository, cfg.PasswordReset.Secret, cfg.PasswordReset.ValidityMinutes,
	)
	sendEmailVerificationCodeHandler := authCommands.NewSendEmailVerificationCodeHandler(
		userRepository, notificationService, cfg.PasswordReset.Secret, cfg.PasswordReset.ValidityMinutes,
	)
	validateEmailHandler := authCommands.NewValidateEmailHandler(
		userRepository, cfg.PasswordReset.Secret, cfg.PasswordReset.ValidityMinutes,
	)
	userIdentityRepository := database.NewDbContext[entities.UserIdentity](client, dbName)
	socialVerifiers, socialKeepers, err := socialAuthAdapters(socialAuthConfig)
	if err != nil {
		return nil, nil, err
	}
	socialAuthHandler := authCommands.NewSocialAuthHandler(
		userRepository, userIdentityRepository, userInfoHistoryRepository, jwtService, socialVerifiers, socialKeepers,
	)
	validateAuthHandler := authQueries.NewValidateAuthHandler(userRepository, houseRepository, userIdentityRepository, jwtService)
	cqrs.MustRegister[string, authCommands.LoginCommand](applicationMediator, loginHandler)
	cqrs.MustRegister[string, authCommands.SignUpCommand](applicationMediator, signUpHandler)
	cqrs.MustRegister[dtos.SocialAuthResponseModel, authCommands.SocialAuthCommand](applicationMediator, socialAuthHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.ForgotPasswordCommand](applicationMediator, forgotPasswordHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.ResetPasswordCommand](applicationMediator, resetPasswordHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.SendEmailVerificationCodeCommand](applicationMediator, sendEmailVerificationCodeHandler)
	cqrs.MustRegister[cqrs.NoResult, authCommands.ValidateEmailCommand](applicationMediator,
		infrastructureAppEvents.NotifyAfter(validateEmailHandler, func(ctx context.Context, command authCommands.ValidateEmailCommand, _ cqrs.NoResult) {
			if user, err := userRepository.FindOneByFilter(ctx, helpers.EmailLookupFilter(command.Email)); err == nil && user != nil {
				appEventService.UsersChanged(ctx, user.Id.Hex())
			}
		}))
	cqrs.MustRegister[*dtos.AuthUserResultModel, authQueries.ValidateAuthQuery](applicationMediator, validateAuthHandler)
	authController := controllers.NewAuthController(applicationMediator, localizationCache)

	authRoutes := api.Group("/auth", middleware.StrictRateLimit(localizationCache))
	authRoutes.Get("/isAuth", authController.IsAuth)
	authRoutes.Post("/login", authController.Login)
	authRoutes.Post("/signup", authController.Signup)
	authRoutes.Post("/social/:provider", authController.SocialAuth)
	authRoutes.Post("/forget", authController.ForgotPassword)
	authRoutes.Post("/reset", authController.ResetPassword)
	authRoutes.Get("/validate-email", middleware.AuthRequired(jwtService, localizationCache), authController.SendEmailVerificationCode)
	authRoutes.Post("/validate-email", middleware.AuthRequired(jwtService, localizationCache), authController.ValidateEmail)
	// ----------

	// - USER -
	imageAssetRepository := database.NewDbContext[entities.ImageAsset](client, dbName)
	houseMembershipPolicy := housePolicies.NewMembershipPolicy(houseRepository)
	imageCache := helpers.NewInMemoryCache[[]dtos.ImageAssetResultModel]()
	gameSessionRepository := database.NewGameSessionRepository(client, dbName)
	matchRepository := database.NewGameMatchRepository(client, dbName)
	// Realtime games need the coordinator; without it no game module is registered.
	gameRegistry, err := games.NewRegistry(games.Options{Realtime: coordinator != nil})
	if err != nil {
		return nil, nil, err
	}
	gameConfigRepository := database.NewGameConfigRepository(client, dbName)
	gameConfigBus, _ := coordinator.(coordinationAbstract.GameConfigBus)
	gameCatalog := gameApplication.NewCatalog(gameRegistry, gameConfigRepository, gameConfigBus)
	reportGameConfig := func(err error) { log.Println("game config:", err) }
	if err := gameCatalog.Refresh(ctx); err != nil {
		reportGameConfig(err)
	}
	go gameCatalog.Run(ctx, reportGameConfig)
	cqrs.MustRegister[[]gameApplication.CatalogEntry, gameQueries.GetGameCatalogQuery](applicationMediator, gameQueries.NewGetGameCatalogHandler(gameCatalog, houseMembershipPolicy))
	cqrs.MustRegister[[]gameApplication.CatalogEntry, gameQueries.GetGameConfigsQuery](applicationMediator, gameQueries.NewGetGameConfigsHandler(gameCatalog))
	cqrs.MustRegister[gameApplication.CatalogEntry, gameCommands.UpdateGameConfigCommand](applicationMediator, gameCommands.NewUpdateGameConfigHandler(gameCatalog, gameConfigRepository, reportGameConfig))
	turnEngine := gameCommands.NewTurnEngine(gameRegistry, matchRepository)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.ApplyTurnMoveCommand](applicationMediator, gameCommands.NewApplyTurnMoveHandler(gameSessionRepository, houseMembershipPolicy, turnEngine))
	cqrs.MustRegister[gameDomain.MatchResult, gameCommands.CompleteMatchCommand](applicationMediator, gameCommands.NewCompleteMatchHandler(gameSessionRepository, matchRepository, gameRegistry))
	cqrs.MustRegister[any, gameQueries.GetMatchResultQuery](applicationMediator, gameQueries.NewGetMatchResultHandler(gameSessionRepository, matchRepository, gameRegistry, houseMembershipPolicy))
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.AuthorizeCancellationQuery](applicationMediator, gameQueries.NewAuthorizeCancellationHandler(gameSessionRepository, houseMembershipPolicy))
	ensureActiveGameSessionHandler := gameCommands.NewEnsureActiveGameSessionHandler(
		gameSessionRepository,
		gameCatalog,
		houseMembershipPolicy,
	)
	joinGameSessionHandler := gameCommands.NewJoinGameSessionHandler(gameSessionRepository, houseMembershipPolicy)
	setPlayerReadyHandler := gameCommands.NewSetPlayerReadyHandler(gameSessionRepository, houseMembershipPolicy)
	leaveGameSessionHandler := gameCommands.NewLeaveGameSessionHandler(gameSessionRepository, houseMembershipPolicy, turnEngine)
	cancelGameSessionHandler := gameCommands.NewCancelGameSessionHandler(gameSessionRepository, houseMembershipPolicy, gameRegistry, turnEngine)
	advanceGameSessionHandler := gameCommands.NewAdvanceGameSessionHandler(gameSessionRepository, turnEngine)
	getGameSessionHandler := gameQueries.NewGetGameSessionHandler(gameSessionRepository, houseMembershipPolicy)
	getActiveGameSessionHandler := gameQueries.NewGetActiveGameSessionHandler(
		gameSessionRepository,
		gameCatalog,
		houseMembershipPolicy,
	)

	createUserHandler := userCommands.NewCreateUserHandler(userRepository, userInfoHistoryRepository)
	deleteUserHandler := userCommands.NewDeleteUserHandler(userRepository, houseRepository, userIdentityRepository, socialKeepers)
	updateProfileHandler := userCommands.NewUpdateProfileHandler(userRepository, userInfoHistoryRepository)
	getUserByEmailHandler := userQueries.NewGetUserByEmailHandler(userRepository)
	listUsersHandler := userQueries.NewListUsersHandler(userRepository)
	getUsersByHouseHandler := userQueries.NewGetUsersByHouseHandler(userRepository, houseMembershipPolicy)
	createImageAssetHandler := imageAssetCommands.NewCreateImageAssetHandler(imageAssetRepository, imageCache)
	updateImageAssetHandler := imageAssetCommands.NewUpdateImageAssetHandler(imageAssetRepository, imageCache)
	getImagesByCategoryHandler := imageAssetQueries.NewGetImagesByCategoryHandler(imageAssetRepository, imageCache)
	getImageByPublicIDHandler := imageAssetQueries.NewGetImageByPublicIDHandler(imageAssetRepository)

	cqrs.MustRegister[*dtos.NewUserModel, userCommands.CreateUserCommand](applicationMediator, createUserHandler)
	cqrs.MustRegister[cqrs.NoResult, userCommands.DeleteUserCommand](applicationMediator,
		appEventService.NotifyDeleteUser(deleteUserHandler, houseRepository))
	cqrs.MustRegister[*dtos.UserResultModel, userCommands.UpdateProfileCommand](applicationMediator,
		infrastructureAppEvents.NotifyAfter(updateProfileHandler, func(ctx context.Context, _ userCommands.UpdateProfileCommand, result *dtos.UserResultModel) {
			appEventService.UsersChanged(ctx, result.Id)
			for _, houseID := range result.HouseIds {
				appEventService.HouseChanged(ctx, houseID, false)
			}
		}))
	cqrs.MustRegister[*dtos.UserResultModel, userQueries.GetUserByEmailQuery](applicationMediator, getUserByEmailHandler)
	cqrs.MustRegister[[]dtos.UserResultModel, userQueries.ListUsersQuery](applicationMediator, listUsersHandler)
	cqrs.MustRegister[[]dtos.UserResultModel, userQueries.GetUsersByHouseQuery](applicationMediator, getUsersByHouseHandler)
	cqrs.MustRegister[cqrs.NoResult, imageAssetCommands.CreateImageAssetCommand](applicationMediator, createImageAssetHandler)
	cqrs.MustRegister[cqrs.NoResult, imageAssetCommands.UpdateImageAssetCommand](applicationMediator, updateImageAssetHandler)
	cqrs.MustRegister[[]dtos.ImageAssetResultModel, imageAssetQueries.GetImagesByCategoryQuery](applicationMediator, getImagesByCategoryHandler)
	cqrs.MustRegister[*dtos.ImageAssetResultModel, imageAssetQueries.GetImageByPublicIDQuery](applicationMediator, getImageByPublicIDHandler)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.EnsureActiveGameSessionCommand](applicationMediator, ensureActiveGameSessionHandler)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.JoinGameSessionCommand](applicationMediator, joinGameSessionHandler)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.SetPlayerReadyCommand](applicationMediator, setPlayerReadyHandler)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.LeaveGameSessionCommand](applicationMediator, leaveGameSessionHandler)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.CancelGameSessionCommand](applicationMediator, cancelGameSessionHandler)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.AdvanceGameSessionCommand](applicationMediator, advanceGameSessionHandler)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.ReconcileGameSessionPlayersCommand](applicationMediator, gameCommands.NewReconcileGameSessionPlayersHandler(gameSessionRepository, turnEngine))
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.GetGameSessionQuery](applicationMediator, getGameSessionHandler)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.GetActiveGameSessionQuery](applicationMediator, getActiveGameSessionHandler)
	userController := controllers.NewUserController(applicationMediator, localizationCache)

	userRoutes := api.Group("/user", middleware.AuthRequired(jwtService, localizationCache), middleware.UserRateLimit(localizationCache))
	userRoutes.Post("", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), userController.NewUser)
	userRoutes.Get("/usersList", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), userController.ListUsers)
	userRoutes.Get("/getByEmail", userController.GetUserByEmail)
	userRoutes.Get("/getUsersByHouse", userController.GetUsersByHouse)
	userRoutes.Get("/getImages", userController.GetImages)
	userRoutes.Get("/getImage", userController.GetImage)
	userRoutes.Post("/images", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), userController.CreateImageAsset)
	userRoutes.Put("/images", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), userController.UpdateImageAsset)
	userRoutes.Put("/profile/:id", userController.UpdateProfile)
	// Registered before "/:id" so "me" is not taken as a user id.
	userRoutes.Delete("/me", userController.DeleteMe)
	userRoutes.Delete("/:id", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), userController.DeleteUser)
	// ----------

	// - HOUSE -
	houseChoreRepository := database.NewDbContext[entities.Chore](client, dbName)
	houseChoreStatusHistoryRepository := database.NewDbContext[entities.ChoreStatusHistory](client, dbName)
	houseChoreReviewVoteRepository := database.NewDbContext[entities.ChoreReviewVote](client, dbName)
	houseAnnouncementRepository := database.NewDbContext[entities.Announcement](client, dbName)
	houseInviteCodeRepository := database.NewDbContext[entities.HouseInviteCode](client, dbName)
	houseInfoHistoryRepository := database.NewDbContext[entities.HouseInfoHistory](client, dbName)
	houseInfoReader := houseApplication.NewInfoReader(userRepository)

	createHouseHandler := housecommands.NewCreateHouseHandler(houseRepository, userRepository)
	generateHouseInviteCodeHandler := housecommands.NewGenerateHouseInviteCodeHandler(
		houseRepository,
		houseInviteCodeRepository,
		cfg.HouseJoinCode.Secret,
		cfg.HouseJoinCode.ValidityMinutes,
	)
	joinHouseHandler := housecommands.NewJoinHouseHandler(
		houseRepository,
		userRepository,
		houseInviteCodeRepository,
		cfg.HouseJoinCode.Secret,
	)
	createAnnouncementHandler := housecommands.NewCreateAnnouncementHandler(
		houseMembershipPolicy,
		userRepository,
		houseAnnouncementRepository,
	)
	updateHouseProfileHandler := housecommands.NewUpdateHouseProfileHandler(
		houseMembershipPolicy,
		houseRepository,
		houseInfoHistoryRepository,
		houseInfoReader,
	)
	exitHouseHandler := housecommands.NewExitHouseHandler(
		houseRepository,
		userRepository,
		houseInviteCodeRepository,
	)
	getHouseInfosHandler := housequeries.NewGetHouseInfosHandler(houseMembershipPolicy, houseInfoReader)
	getHouseDetailsHandler := housequeries.NewGetHouseDetailsHandler(
		houseMembershipPolicy,
		houseRepository,
		userRepository,
		houseChoreRepository,
		houseChoreStatusHistoryRepository,
		houseChoreReviewVoteRepository,
		houseAnnouncementRepository,
	)
	cqrs.MustRegister[*entities.House, housecommands.CreateHouseCommand](applicationMediator,
		infrastructureAppEvents.NotifyAfter(createHouseHandler, func(ctx context.Context, _ housecommands.CreateHouseCommand, result *entities.House) {
			appEventService.HouseChanged(ctx, result.Id.Hex(), true)
		}))
	cqrs.MustRegister[*dtos.HouseInviteCodeResponseModel, housecommands.GenerateHouseInviteCodeCommand](applicationMediator, generateHouseInviteCodeHandler)
	cqrs.MustRegister[*entities.House, housecommands.JoinHouseCommand](applicationMediator,
		infrastructureAppEvents.NotifyAfter(joinHouseHandler, func(ctx context.Context, command housecommands.JoinHouseCommand, result *entities.House) {
			appEventService.HouseChanged(ctx, result.Id.Hex(), false)
			appEventService.UsersChanged(ctx, command.UserID)
		}))
	cqrs.MustRegister[*dtos.AnnouncementResponseModel, housecommands.CreateAnnouncementCommand](applicationMediator,
		infrastructureAppEvents.NotifyAfter(createAnnouncementHandler, func(ctx context.Context, command housecommands.CreateAnnouncementCommand, _ *dtos.AnnouncementResponseModel) {
			appEventService.HouseChanged(ctx, command.HouseID, false)
		}))
	cqrs.MustRegister[*dtos.HouseInfosResponseModel, housecommands.UpdateHouseProfileCommand](applicationMediator,
		infrastructureAppEvents.NotifyAfter(updateHouseProfileHandler, func(ctx context.Context, command housecommands.UpdateHouseProfileCommand, _ *dtos.HouseInfosResponseModel) {
			appEventService.HouseChanged(ctx, command.HouseID, true)
		}))
	cqrs.MustRegister[cqrs.NoResult, housecommands.ExitHouseCommand](applicationMediator,
		infrastructureAppEvents.NotifyAfter(exitHouseHandler, func(ctx context.Context, command housecommands.ExitHouseCommand, _ cqrs.NoResult) {
			appEventService.HouseChanged(ctx, command.HouseID, false)
			appEventService.UsersChanged(ctx, command.TargetUserID)
		}))
	cqrs.MustRegister[*dtos.HouseInfosResponseModel, housequeries.GetHouseInfosQuery](applicationMediator, getHouseInfosHandler)
	cqrs.MustRegister[*dtos.HouseDetailsModel, housequeries.GetHouseDetailsQuery](applicationMediator, getHouseDetailsHandler)
	houseController := controllers.NewHouseController(
		applicationMediator,
		localizationCache,
	)

	houseRoutes := api.Group("/house", middleware.AuthRequired(jwtService, localizationCache), middleware.UserRateLimit(localizationCache))
	houseRoutes.Get("/details", houseController.GetHouseDetails)
	houseRoutes.Get("/infos", houseController.GetHouseInfos)
	houseRoutes.Post("/announcement", houseController.CreateAnnouncement)
	houseRoutes.Post("/create", houseController.CreateHouse)
	houseRoutes.Post("/inviteCode", houseController.GenerateInviteCode)
	houseRoutes.Post("/join", houseController.JoinHouseByCode)
	houseRoutes.Put("/profile", houseController.UpdateHouseProfile)
	houseRoutes.Post("/exit", houseController.ExitHouse)
	// ----------

	// - CHORE -
	choreAssignmentPolicy := chorePolicies.NewAssignmentPolicy(userRepository)
	choreWorkflowPolicy := chorePolicies.NewWorkflowPolicy()
	createChoreHandler := choreCommands.NewCreateChoreHandler(
		houseChoreRepository,
		houseChoreStatusHistoryRepository,
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
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.CreateChoreCommand](applicationMediator,
		infrastructureAppEvents.NotifyAfter(createChoreHandler, func(ctx context.Context, _ choreCommands.CreateChoreCommand, result *dtos.ChoreResponseModel) {
			appEventService.HouseChanged(ctx, result.HouseId, false)
		}))
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.UpdateChoreCommand](applicationMediator,
		infrastructureAppEvents.NotifyAfter(updateChoreHandler, func(ctx context.Context, _ choreCommands.UpdateChoreCommand, result *dtos.ChoreResponseModel) {
			appEventService.HouseChanged(ctx, result.HouseId, false)
		}))
	cqrs.MustRegister[[]dtos.ChoreResponseModel, choreCommands.UpdateChoreStatusCommand](applicationMediator,
		infrastructureAppEvents.NotifyAfter(updateChoreStatusHandler, func(ctx context.Context, command choreCommands.UpdateChoreStatusCommand, result []dtos.ChoreResponseModel) {
			if len(result) > 0 {
				appEventService.HouseChanged(ctx, command.HouseID, false)
			}
		}))
	cqrs.MustRegister[*dtos.ChoreResponseModel, choreCommands.ReviewChoreCommand](applicationMediator,
		infrastructureAppEvents.NotifyAfter(reviewChoreHandler, func(ctx context.Context, _ choreCommands.ReviewChoreCommand, result *dtos.ChoreResponseModel) {
			appEventService.HouseChanged(ctx, result.HouseId, false)
		}))
	choreController := controllers.NewChoreController(applicationMediator, localizationCache)

	choreRoutes := api.Group("/chore", middleware.AuthRequired(jwtService, localizationCache), middleware.UserRateLimit(localizationCache))
	choreRoutes.Post("", choreController.CreateChore)
	choreRoutes.Put("/status", choreController.UpdateChoreStatus)
	choreRoutes.Put("/review", choreController.ReviewChore)
	choreRoutes.Put("/:id", choreController.UpdateChore)
	// ----------
	gameController := controllers.NewGameController(applicationMediator, localizationCache)
	gameRoutes := api.Group(
		"/game",
		middleware.AuthRequired(jwtService, localizationCache),
	)
	gameRoutes.Get("/catalog", middleware.UserRateLimit(localizationCache), gameController.GetCatalog)
	gameRoutes.Get("/configs", middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), gameController.GetConfigs)
	gameRoutes.Put("/:gameKey/config", middleware.UserRateLimit(localizationCache), middleware.RequireRoleWithLocalizer(localizationCache, int(entities.SuperAdmin)), gameController.UpdateConfig)
	gameRoutes.Put(
		"/:gameKey/session",
		middleware.UserRateLimit(localizationCache),
		gameController.EnsureActiveSession,
	)
	gameRoutes.Get(
		"/:gameKey/session",
		middleware.UserRateLimit(localizationCache),
		gameController.GetActiveSession,
	)
	// /game/:sessionId/{result,realtime} are kept as aliases for released clients.
	for _, path := range []string{"/sessions/:sessionId/result", "/:sessionId/result"} {
		gameRoutes.Get(path, middleware.UserRateLimit(localizationCache), gameController.GetResult)
	}
	realtimePaths := []string{"/sessions/:sessionId/realtime", "/:sessionId/realtime"}

	if coordinator == nil {
		for _, path := range realtimePaths {
			gameRoutes.Get(path, func(c *fiber.Ctx) error {
				return helpers.RespondLocalizedError(
					c,
					localizationCache,
					helpers.NewUnavailableError("realtime.error.unavailable", coordinationAbstract.ErrUnavailable),
				)
			})
		}
		return nil, appEventService, nil
	}
	limits, err := config.LoadRealtimeLimits()
	if err != nil {
		return nil, nil, err
	}
	realtimeService, err := realtime.NewService(
		coordinator,
		gameSessionRepository,
		applicationMediator,
		realtime.RoomManagerOptions{Games: gameRegistry, ParticipantDirectory: database.NewGameParticipantDirectory(client, dbName), MatchRepository: matchRepository, MaxOwnedRooms: limits.MaxOwnedRooms},
		realtime.GatewayOptions{
			AllowedOrigins: webSocketAllowedOrigins(),
			Localizer:      localizationCache,
			MaxConnections: limits.MaxConnections,
		},
	)
	if err != nil {
		return nil, nil, err
	}
	if err := realtimeService.Start(ctx); err != nil {
		return nil, nil, err
	}
	for _, path := range realtimePaths {
		gameRoutes.Get(path, realtimeService.UpgradeMiddleware(), realtimeService.Handler())
	}
	appEventService.Start(ctx)
	return realtimeService, appEventService, nil
}
