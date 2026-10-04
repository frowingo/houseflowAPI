package tests

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestMigrationsRunOutsideAPIStartup(t *testing.T) {
	repositoryRoot := repositoryRoot(t)
	apiMain := readContractFile(t, filepath.Join(repositoryRoot, "cmd", "api", "main.go"))
	migrationCommand := readContractFile(t, filepath.Join(repositoryRoot, "cmd", "api", "migrate.go"))
	flyConfig := readContractFile(t, filepath.Join(repositoryRoot, "fly.toml"))

	if strings.Contains(apiMain, "migration.RunAll") {
		t.Fatal("API startup still runs database migrations")
	}
	if !strings.Contains(migrationCommand, "migrations.RunAll") {
		t.Fatal("migration command does not run registered migrations")
	}
	if !strings.Contains(flyConfig, "release_command = './houseflowapi migrate'") {
		t.Fatal("Fly deployment does not use the migration release command")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(workingDirectory, ".."))
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
