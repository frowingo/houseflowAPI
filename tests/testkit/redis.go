package testkit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

const CoordinationTestTimeout = 5 * time.Second

func PrepareRedis(t *testing.T) string {
	t.Helper()
	redisURL := os.Getenv("HOUSEFLOW_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("HOUSEFLOW_TEST_REDIS_URL is required for coordination integration tests")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx, cancel := context.WithTimeout(context.Background(), CoordinationTestTimeout)
	t.Cleanup(cancel)
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush Redis database %d (each test package uses its own, see redisSlots): %v", options.DB, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_ = client.FlushDB(cleanupCtx).Err()
		_ = client.Close()
	})
	return redisURL
}
