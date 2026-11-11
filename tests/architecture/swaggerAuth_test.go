package architecture

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"houseflowApi/internal/infrastructure/middleware"

	"github.com/gofiber/fiber/v2"
)

func TestSwaggerAuthAdmitsOnlyTheRequiredRole(t *testing.T) {
	const superAdmin = 2
	login := func(_ context.Context, email, password string) (int, error) {
		if password != "right" {
			return 0, errors.New("invalid password")
		}
		if email == "admin@example.com" {
			return superAdmin, nil
		}
		return 0, nil
	}
	app := fiber.New()
	app.Get("/swagger/*", middleware.SwaggerAuth(login, superAdmin), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	for _, tc := range []struct {
		name, email, password string
		want                  int
	}{
		{"no credentials", "", "", fiber.StatusUnauthorized},
		{"wrong password", "admin@example.com", "wrong", fiber.StatusUnauthorized},
		{"normal user", "user@example.com", "right", fiber.StatusUnauthorized},
		{"super admin", "admin@example.com", "right", fiber.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(fiber.MethodGet, "/swagger/index.html", nil)
			if tc.email != "" {
				request.SetBasicAuth(tc.email, tc.password)
			}
			response, err := app.Test(request, -1)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.want)
			}
			if tc.want == fiber.StatusUnauthorized && response.Header.Get(fiber.HeaderWWWAuthenticate) == "" {
				t.Fatal("missing WWW-Authenticate header, the browser would not prompt")
			}
		})
	}
}
