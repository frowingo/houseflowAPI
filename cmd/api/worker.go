package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"houseflowApi/internal/adaptors"
	"houseflowApi/internal/config"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/database"
)

// workerHealthAddress serves only health checks; the worker has no public API.
const workerHealthAddress = ":3163"

// runWorker processes background jobs without serving the API. It registers
// the same handlers as serve (setupBackgroundJobs) and requires Valkey: an
// API group with FAUSCREW_ROLE=api only enqueues, and workers drain the queue.
func runWorker() error {
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.MustLoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg.Process.Role = config.ProcessRoleWorker

	startupCtx, cancelStartup := context.WithTimeout(rootCtx, startupTimeout)
	mongoClient, db, err := database.NewDatabase(startupCtx, cfg.External.Mongo)
	cancelStartup()
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer disconnectDatabase(mongoClient)

	// Jobs publish house and notification events to the API instances' sockets.
	coordinator := adaptors.NewCoordinator(rootCtx, cfg.External.Redis, cfg.Process)
	defer adaptors.CloseCoordinator(coordinator)

	jobRuntime, err := adaptors.NewWorkerJobRuntime(cfg.External.Redis)
	if err != nil {
		return err
	}
	services, err := setupBackgroundJobs(
		rootCtx, mongoClient, db.Name(), cfg.Internal, cfg.External.Firebase, coordinator, jobRuntime, nil,
	)
	if err != nil {
		adaptors.CloseJobRuntime(jobRuntime)
		return fmt.Errorf("set up background jobs: %w", err)
	}
	defer closeAppEventService(services.appEvents)
	if err := jobRuntime.Start(rootCtx); err != nil {
		adaptors.CloseJobRuntime(jobRuntime)
		return fmt.Errorf("start background jobs: %w", err)
	}
	// Deferred last, so running jobs finish before the services they use close.
	defer adaptors.CloseJobRuntime(jobRuntime)
	go adaptors.ObserveJobs(rootCtx, jobRuntime, cfg.Process.InstanceID, nil, nil)

	health := fiber.New(fiber.Config{
		AppName:               "FausCrew worker",
		DisableStartupMessage: true,
		ReadTimeout:           5 * time.Second,
		WriteTimeout:          5 * time.Second,
	})
	healthRoutes := health.Group("/api/v1/base")
	healthRoutes.Get("/health/live", controllers.HealthController)
	healthRoutes.Get("/health/ready", controllers.ReadinessController(func(ctx context.Context) error {
		return mongoClient.Ping(ctx, readpref.Primary())
	}))
	healthRoutes.Get("/health/jobs", controllers.ReadinessController(jobRuntime.Ping))

	listenError := make(chan error, 1)
	go func() {
		listenError <- health.Listen(workerHealthAddress)
	}()
	log.Printf("worker started (instance=%s)", cfg.Process.InstanceID)

	select {
	case err := <-listenError:
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case <-rootCtx.Done():
		log.Println("shutdown signal received")
	}
	// Stop taking new jobs at once; running ones finish in CloseJobRuntime.
	jobRuntime.Stop()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	if err := health.ShutdownWithContext(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown health server: %w", err)
	}
	return nil
}
