// Package gamekit holds the game session, room runtime and realtime gateway
// fixtures shared by the game, gamecore and realtime test packages.
package gamekit

import (
	"context"
	"testing"
	"time"

	infrastructureCoordination "houseflowApi/internal/infrastructure/coordination"
)

func NewTestCoordinator(
	t *testing.T,
	redisURL string,
	instanceID string,
) *infrastructureCoordination.RedisCoordinator {
	t.Helper()
	coordinator, err := infrastructureCoordination.NewRedisCoordinator(
		infrastructureCoordination.RedisOptions{
			URL:               redisURL,
			InstanceID:        instanceID,
			HeartbeatTTL:      600 * time.Millisecond,
			HeartbeatInterval: 100 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.Start(context.Background())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = coordinator.Close(ctx)
	})
	return coordinator
}

func Eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}
