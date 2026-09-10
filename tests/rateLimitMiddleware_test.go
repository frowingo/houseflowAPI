package tests

import (
	"encoding/json"
	"houseflowApi/internal/infrastructure/middleware"
	"houseflowApi/internal/models/core"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestRateLimitUsesStandardErrorResponse(t *testing.T) {
	app := fiber.New()
	app.Use(middleware.RateLimit(middleware.RateLimitConfig{Max: 0, Window: time.Minute}))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	response, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusTooManyRequests)
	}
	var body core.ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Success || body.Error != "rate_limit.error.too_many_requests" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

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
