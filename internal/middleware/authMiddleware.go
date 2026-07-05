package middleware

import (
	"houseflowApi/internal/helpers"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func AuthRequired(localizers ...helpers.MessageLocalizer) fiber.Handler {
	var localizer helpers.MessageLocalizer
	if len(localizers) > 0 {
		localizer = localizers[0]
	}

	return func(c *fiber.Ctx) error {

		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": helpers.LocalizedMessage(c, localizer, "auth.error.authorization_header_required"),
			})
		}

		// "Bearer <token>" formatını kontrol et
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": helpers.LocalizedMessage(c, localizer, "auth.error.invalid_authorization_format"),
			})
		}

		token := parts[1]

		jwtData, err := helpers.ValidateToken(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": helpers.LocalizedMessage(c, localizer, "auth.error.invalid_or_expired_token"),
			})
		}

		// User bilgisini context'e kaydet
		c.Locals("userEmail", jwtData.Issuer)
		c.Locals("userID", jwtData.Subject)
		c.Locals("userRole", jwtData.IssuerRole)
		c.Locals("language", helpers.NormalizeLanguage(jwtData.Language))

		return c.Next()
	}
}

func RequireRole(requiredRoles ...int) fiber.Handler {
	return RequireRoleWithLocalizer(nil, requiredRoles...)
}

func RequireRoleWithLocalizer(localizer helpers.MessageLocalizer, requiredRoles ...int) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userRole, ok := c.Locals("userRole").(int)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": helpers.LocalizedMessage(c, localizer, "auth.error.unauthorized"),
			})
		}

		for _, role := range requiredRoles {
			if userRole == role {
				return c.Next()
			}
		}

		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": helpers.LocalizedMessage(c, localizer, "auth.error.insufficient_role"),
		})
	}
}
