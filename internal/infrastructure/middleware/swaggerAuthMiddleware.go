package middleware

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/basicauth"
)

const swaggerLoginTimeout = 10 * time.Second

// SwaggerLogin checks an email and password and returns the account's role.
type SwaggerLogin func(ctx context.Context, email, password string) (role int, err error)

// SwaggerAuth puts Swagger UI behind HTTP Basic auth, the only scheme a
// browser offers on plain page navigation. Only requiredRole may open it.
func SwaggerAuth(login SwaggerLogin, requiredRole int) fiber.Handler {
	return basicauth.New(basicauth.Config{
		Realm: "HouseFlow Swagger",
		Authorizer: func(email, password string) bool {
			ctx, cancel := context.WithTimeout(context.Background(), swaggerLoginTimeout)
			defer cancel()
			role, err := login(ctx, email, password)
			return err == nil && role == requiredRole
		},
	})
}
