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
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/swagger"

	docs "houseflowApi/external/swagger/docs" // Swagger docs
	"houseflowApi/internal/config"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/infrastructure/realtime"
)

const (
	startupTimeout   = 15 * time.Second
	shutdownTimeout  = 10 * time.Second
	requestBodyLimit = 4 * 1024 * 1024
)

// @title HouseFlow API
// @version 1.0
// @description HouseFlow API Documentation
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.email support@houseflow.com

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @host localhost:3162
// @BasePath /api/v1
// @schemes http https

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.
func main() {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	var err error
	switch command {
	case "serve":
		err = runAPI()
	case "migrate":
		err = runMigrations()
	default:
		err = fmt.Errorf("unknown command %q; expected serve or migrate", command)
	}

	if err != nil {
		log.Printf("application stopped with error: %v", err)
		os.Exit(1)
	}
}

func runAPI() error {
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.MustLoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	startupCtx, cancelStartup := context.WithTimeout(rootCtx, startupTimeout)
	mongoClient, db, err := database.NewDatabase(startupCtx, cfg.External.Mongo)
	cancelStartup()
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer disconnectDatabase(mongoClient)

	coordinator := initializeCoordinator(rootCtx, cfg.External.Redis)
	defer closeCoordinator(coordinator)

	// Host'u boş bırakarak Swagger UI'nin isteğin geldiği host/scheme'i
	// kullanmasını sağla (localhost, OrbStack domain, vs. ile uyumlu).
	docs.SwaggerInfo.Host = ""
	docs.SwaggerInfo.Schemes = []string{}

	app := fiber.New(fiber.Config{
		AppName:      "HouseFlow API",
		ProxyHeader:  fiber.HeaderXForwardedFor,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
		BodyLimit:    requestBodyLimit,
	})

	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: getAllowedOrigins(),
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET, POST, PUT, DELETE, OPTIONS",
	}))

	app.Get("/swagger/*", swagger.HandlerDefault)
	app.Get(
		"/api/v1/base/health/coordination",
		controllers.ReadinessController(coordinationReadiness(coordinator)),
	)

	roomManager, err := SetupRoutes(rootCtx, app, mongoClient, db.Name(), cfg.Internal, coordinator)
	if err != nil {
		return fmt.Errorf("initialize realtime room runtime: %w", err)
	}
	defer closeRoomManager(roomManager)

	listenError := make(chan error, 1)
	go func() {
		listenError <- app.Listen(":3162")
	}()

	select {
	case err := <-listenError:
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case <-rootCtx.Done():
		log.Println("shutdown signal received")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}

	select {
	case err := <-listenError:
		if err != nil {
			return fmt.Errorf("listen shutdown: %w", err)
		}
	case <-shutdownCtx.Done():
		return fmt.Errorf("wait for HTTP server shutdown: %w", shutdownCtx.Err())
	}

	return nil
}

func closeRoomManager(manager *realtime.RoomManager) {
	if manager == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := manager.Close(ctx); err != nil && err != context.Canceled {
		log.Printf("realtime room runtime shutdown failed: %v", err)
	}
}

func disconnectDatabase(client interface{ Disconnect(context.Context) error }) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Disconnect(ctx); err != nil {
		log.Printf("database disconnect failed: %v", err)
	}
}

func getAllowedOrigins() string {
	if origins := os.Getenv("CORS_ALLOW_ORIGINS"); origins != "" {
		return origins
	}
	if os.Getenv("APP_ENV") == "production" {
		log.Println("CORS_ALLOW_ORIGINS is empty in production; using Fiber's default origin policy")
		return ""
	}
	return "*"
}
