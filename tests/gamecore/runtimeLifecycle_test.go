package gamecore

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"houseflowApi/internal/controllers"

	"github.com/gofiber/fiber/v2"
)

func TestLivenessEndpointDoesNotDependOnExternalServices(t *testing.T) {
	app := fiber.New()
	app.Get("/health/live", controllers.HealthController)

	response, err := app.Test(httptest.NewRequest("GET", "/health/live", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("liveness status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
}

func TestReadinessEndpointReflectsDependencyState(t *testing.T) {
	tests := []struct {
		name       string
		check      controllers.ReadinessCheck
		wantStatus int
	}{
		{
			name: "ready",
			check: func(ctx context.Context) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("readiness check context has no deadline")
				}
				return nil
			},
			wantStatus: fiber.StatusOK,
		},
		{
			name: "dependency unavailable",
			check: func(context.Context) error {
				return errors.New("dependency unavailable")
			},
			wantStatus: fiber.StatusServiceUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/health/ready", controllers.ReadinessController(test.check))

			response, err := app.Test(httptest.NewRequest("GET", "/health/ready", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()

			if response.StatusCode != test.wantStatus {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("readiness status = %d, want %d; body=%s", response.StatusCode, test.wantStatus, body)
			}
		})
	}
}
