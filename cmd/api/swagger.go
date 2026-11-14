package main

import (
	"context"
	"os"

	authCommands "houseflowApi/internal/application/auth/commands"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
)

// registerSwagger serves Swagger UI: open outside production, and in
// production only to a SuperAdmin signing in with email and password.
func registerSwagger(
	app *fiber.App,
	loginHandler *authCommands.LoginHandler,
	jwtService *helpers.JWTService,
	rateLimit fiber.Handler,
) {
	if os.Getenv("APP_ENV") != "production" {
		app.Get("/swagger/*", swagger.HandlerDefault)
		return
	}
	// Going through the login command keeps its rules in one place: wrong
	// passwords count toward the account lock, locked accounts are refused.
	login := func(ctx context.Context, email, password string) (int, error) {
		token, err := loginHandler.Handle(ctx, authCommands.LoginCommand{Email: email, Password: password})
		if err != nil {
			return 0, err
		}
		claims, err := jwtService.ValidateToken(token)
		return claims.IssuerRole, err
	}
	app.Get("/swagger/*", rateLimit, middleware.SwaggerAuth(login, int(entities.SuperAdmin)), swagger.HandlerDefault)
}
