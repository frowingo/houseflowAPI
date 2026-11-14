package architecture

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"houseflowApi/internal/infrastructure/middleware"
	"houseflowApi/internal/models/core"

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

// isAuth runs on every app start and user.changed; housemates behind one NAT
// must not share the strict /auth budget for it.
func TestIsAuthIsOutsideStrictAuthRateLimit(t *testing.T) {
	routes := readContractFile(t, filepath.Join(repositoryRoot(t), "cmd", "api", "routes.go"))
	isAuth := strings.Index(routes, `api.Get("/auth/isAuth", authController.IsAuth)`)
	authGroup := strings.Index(routes, `api.Group("/auth", middleware.StrictRateLimit(`)
	if isAuth < 0 || authGroup < 0 || isAuth > authGroup {
		t.Fatal("routes.go must register /auth/isAuth on api before the strict /auth group")
	}
	if strings.Contains(routes, `authRoutes.Get("/isAuth"`) {
		t.Fatal("/isAuth is still registered in the strict /auth group")
	}

	// Same layout as routes.go: a route registered before a group's Use-style
	// middleware is matched first and never reaches it.
	app := fiber.New()
	api := app.Group("/api/v1")
	api.Get("/auth/isAuth", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })
	authRoutes := api.Group("/auth", middleware.StrictRateLimit())
	authRoutes.Post("/login", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	status := func(method, path string) int {
		response, err := app.Test(httptest.NewRequest(method, path, nil), -1)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	for i := 0; i < 15; i++ {
		if got := status(fiber.MethodGet, "/api/v1/auth/isAuth"); got != fiber.StatusNoContent {
			t.Fatalf("isAuth request %d status=%d, want %d", i+1, got, fiber.StatusNoContent)
		}
	}
	for i := 0; i < 10; i++ {
		if got := status(fiber.MethodPost, "/api/v1/auth/login"); got != fiber.StatusNoContent {
			t.Fatalf("login request %d status=%d, want %d", i+1, got, fiber.StatusNoContent)
		}
	}
	if got := status(fiber.MethodPost, "/api/v1/auth/login"); got != fiber.StatusTooManyRequests {
		t.Fatalf("11th login status=%d, want %d", got, fiber.StatusTooManyRequests)
	}
}
