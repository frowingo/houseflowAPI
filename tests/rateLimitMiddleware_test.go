package tests

import (
	"houseflowApi/internal/middleware"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestRateLimitCountsConcurrentRequestsExactly(t *testing.T) {
	app := fiber.New()
	app.Use(middleware.RateLimit(middleware.RateLimitConfig{Max: 10, Window: time.Minute, KeyFunc: func(*fiber.Ctx) string { return "same-user" }}))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	statuses := make([]int, 50)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range statuses {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			response, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/", nil), -1)
			if err != nil {
				statuses[i] = -1
				return
			}
			statuses[i] = response.StatusCode
		}(i)
	}
	close(start)
	workers.Wait()

	allowed, rejected := 0, 0
	for _, status := range statuses {
		switch status {
		case fiber.StatusNoContent:
			allowed++
		case fiber.StatusTooManyRequests:
			rejected++
		default:
			t.Fatalf("unexpected response status %d", status)
		}
	}
	if allowed != 10 || rejected != 40 {
		t.Fatalf("allowed=%d rejected=%d, want 10 and 40", allowed, rejected)
	}
}
