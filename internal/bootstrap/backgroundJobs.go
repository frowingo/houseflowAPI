package bootstrap

import (
	"context"
	"log"

	"go.mongodb.org/mongo-driver/mongo"

	authEmail "houseflowApi/internal/application/auth/email"
	choreApplication "houseflowApi/internal/application/chore"
	chorePolicies "houseflowApi/internal/application/chore/policies"
	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	"houseflowApi/internal/application/notification"
	"houseflowApi/internal/bootstrap/adaptors"
	"houseflowApi/internal/config"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	infrastructureAppEvents "houseflowApi/internal/infrastructure/appEvents"
	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
	infrastructureLocalization "houseflowApi/internal/infrastructure/localization"
	"houseflowApi/internal/services"
)

// backgroundJobServices are the services both the job handlers and the HTTP
// routes use. The API builds them through SetupRoutes; the worker builds
// only these, so both register exactly the same handlers.
type backgroundJobServices struct {
	localization  *infrastructureLocalization.Cache
	appEvents     *infrastructureAppEvents.Service
	notifications *services.NotificationService
	emailSender   *authEmail.QueuedSender
}

// setupBackgroundJobs registers every job handler and periodic job on the
// runtime; call it before the runtime starts. New job types go here, never
// in SetupRoutes, or the worker would not know them.
func setupBackgroundJobs(
	ctx context.Context,
	client *mongo.Client,
	dbName string,
	cfg config.ConfigInternal,
	firebaseConfig config.ConfigFirebase,
	coordinator coordinationAbstract.Coordinator,
	jobRuntime infrastructureJobs.Runtime,
	webSocketOrigins []string,
) (*backgroundJobServices, error) {
	localizationRepository := database.NewDbContext[entities.Localization](client, dbName)
	localizationCache := infrastructureLocalization.NewCache(localizationRepository)
	if err := localizationCache.Load(ctx); err != nil {
		log.Println("localization cache warmup failed:", err)
	}

	userRepository := database.NewDbContext[entities.User](client, dbName)
	houseRepository := database.NewDbContext[entities.House](client, dbName)
	// The worker only publishes app events; the API also serves the socket.
	appEventBus, _ := coordinator.(coordinationAbstract.AppEventBus)
	appEventService := infrastructureAppEvents.NewService(appEventBus, houseRepository, userRepository, webSocketOrigins)

	notificationRepository := database.NewDbContext[entities.Notification](client, dbName)
	deviceTokenRepository := database.NewDbContext[entities.DeviceToken](client, dbName)
	pushSender, err := adaptors.NewPushSender(ctx, firebaseConfig)
	if err != nil {
		return nil, err
	}
	notificationService := services.NewNotificationService(cfg.SMTP, services.NotificationDelivery{
		Notifications: notificationRepository,
		Houses:        houseRepository,
		Events:        appEventService,
		Jobs:          jobRuntime,
	}, services.WithEmailLocalizer(localizationCache))
	// Push notifications are fanned out to one job per device.
	notification.NewDelivery(
		notificationRepository, deviceTokenRepository, userRepository,
		pushSender, localizationCache, jobRuntime,
	).Register(jobRuntime)
	// Code emails go through the job queue; the SMTP send runs in a job.
	emailSender := authEmail.NewQueuedSender(jobRuntime, notificationService)
	emailSender.Register(jobRuntime)

	choreApplication.NewDeadlineService(
		database.NewDbContext[entities.Chore](client, dbName),
		database.NewDbContext[entities.ChoreStatusHistory](client, dbName),
		chorePolicies.NewWorkflowPolicy(),
		func(ctx context.Context, houseID string) {
			appEventService.HouseChanged(ctx, houseID, false)
		},
	).Register(jobRuntime, jobRuntime)

	return &backgroundJobServices{
		localization:  localizationCache,
		appEvents:     appEventService,
		notifications: notificationService,
		emailSender:   emailSender,
	}, nil
}
