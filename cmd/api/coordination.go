package main

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	"houseflowApi/internal/config"
	infrastructureCoordination "houseflowApi/internal/infrastructure/coordination"
)

func initializeCoordinator(
	ctx context.Context,
	redisConfig config.ConfigRedis,
) coordinationAbstract.Coordinator {
	if redisConfig.URL == "" {
		log.Println("coordination is disabled because REDIS_URL is empty")
		return nil
	}

	instanceBase := os.Getenv("INSTANCE_ID")
	if instanceBase == "" {
		instanceBase = os.Getenv("FLY_MACHINE_ID")
	}
	coordinator, err := infrastructureCoordination.NewRedisCoordinator(
		infrastructureCoordination.RedisOptions{
			URL:        redisConfig.URL,
			InstanceID: infrastructureCoordination.NewInstanceID(instanceBase),
		},
	)
	if err != nil {
		log.Printf("coordination initialization failed; realtime features are unavailable: %v", err)
		return nil
	}
	coordinator.Start(ctx)
	log.Printf("coordination instance started: %s", coordinator.InstanceID())
	return coordinator
}

func coordinationReadiness(coordinator coordinationAbstract.Coordinator) func(context.Context) error {
	return func(ctx context.Context) error {
		if coordinator == nil {
			return coordinationAbstract.ErrUnavailable
		}
		return coordinator.Ping(ctx)
	}
}

func closeCoordinator(coordinator coordinationAbstract.Coordinator) {
	if coordinator == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := coordinator.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("coordination shutdown failed: %v", err)
	}
}
