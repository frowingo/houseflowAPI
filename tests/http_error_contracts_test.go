package tests

import (
	"encoding/json"
	"errors"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/core"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestApplicationErrorsMapToHTTPContract(t *testing.T) {
	testCases := []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
	}{
		{name: "bad request", err: helpers.NewLocalizedError("request.invalid"), wantStatus: fiber.StatusBadRequest, wantError: "request.invalid"},
		{name: "not found", err: helpers.NewNotFoundError("resource.not_found"), wantStatus: fiber.StatusNotFound, wantError: "resource.not_found"},
		{name: "forbidden", err: helpers.NewForbiddenError("resource.forbidden"), wantStatus: fiber.StatusForbidden, wantError: "resource.forbidden"},
		{name: "conflict", err: helpers.NewConflictError("resource.conflict"), wantStatus: fiber.StatusConflict, wantError: "resource.conflict"},
		{name: "rate limited", err: helpers.NewRateLimitError("resource.rate_limited"), wantStatus: fiber.StatusTooManyRequests, wantError: "resource.rate_limited"},
		{name: "unavailable", err: helpers.NewUnavailableError("resource.unavailable", errors.New("connection failed")), wantStatus: fiber.StatusServiceUnavailable, wantError: "resource.unavailable"},
		{name: "unknown errors are hidden", err: errors.New("database credentials leaked"), wantStatus: fiber.StatusInternalServerError, wantError: "common.error.internal_server_error"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", func(c *fiber.Ctx) error {
				return helpers.RespondLocalizedError(c, nil, testCase.err)
			})

			response, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/", nil), -1)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()

			if response.StatusCode != testCase.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, testCase.wantStatus)
			}
			var body core.ErrorResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Success || body.Error != testCase.wantError {
				t.Fatalf("response = %+v, want success=false error=%q", body, testCase.wantError)
			}
		})
	}
}
