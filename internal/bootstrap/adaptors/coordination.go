// Package adaptors builds the infrastructure adapters the API runs with from
// its configuration, and decides what to fall back to when one is missing.
// Only internal/bootstrap calls these; the adapters themselves stay config-free.
package adaptors

import (
	"context"
	"errors"
	"log"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	"houseflowApi/internal/config"
	infrastructureCoordination "houseflowApi/internal/infrastructure/coordination"
)

const coordinatorCloseTimeout = 6 * time.Second

// NewCoordinator connects to Valkey for multi-instance coordination. Without
// REDIS_URL, or when the connection cannot be set up, it returns nil and the
// realtime features are unavailable.
func NewCoordinator(
	ctx context.Context,
	redisConfig config.ConfigRedis,
	process config.ConfigProcess,
) coordinationAbstract.Coordinator {
	if redisConfig.URL == "" {
		log.Println("coordination is disabled because REDIS_URL is empty")
		return nil
	}

	coordinator, err := infrastructureCoordination.NewRedisCoordinator(
		infrastructureCoordination.RedisOptions{
			URL:        redisConfig.URL,
			InstanceID: infrastructureCoordination.NewInstanceID(process.InstanceID),
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

// CoordinationReadiness reports a missing coordinator as unavailable.
func CoordinationReadiness(coordinator coordinationAbstract.Coordinator) func(context.Context) error {
	return func(ctx context.Context) error {
		if coordinator == nil {
			return coordinationAbstract.ErrUnavailable
		}
		return coordinator.Ping(ctx)
	}
}

func CloseCoordinator(coordinator coordinationAbstract.Coordinator) {
	if coordinator == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), coordinatorCloseTimeout)
	defer cancel()
	if err := coordinator.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("coordination shutdown failed: %v", err)
	}
}
