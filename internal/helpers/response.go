package helpers

import (
	"fmt"
	"houseflowApi/internal/models/core"
	"strings"

	"github.com/gofiber/fiber/v2"
)

type MessageLocalizer interface {
	LocalizeMessage(language string, keyOrMessage string) string
}

func LocalizedMessage(c *fiber.Ctx, localizer MessageLocalizer, keyOrMessage string) string {
	language := RequestLanguage(c)
	if localizer == nil {
		key, args := SplitLocalizationMessage(keyOrMessage)
		if len(args) == 0 {
			return key
		}
		return fmt.Sprintf("%s: %s", key, strings.Join(args, ", "))
	}
	return localizer.LocalizeMessage(language, keyOrMessage)
}

func LocalizedErrorMap(c *fiber.Ctx, localizer MessageLocalizer, keyOrMessage string) fiber.Map {
	return fiber.Map{"error": LocalizedMessage(c, localizer, keyOrMessage)}
}

func LocalizedMessageMap(c *fiber.Ctx, localizer MessageLocalizer, keyOrMessage string) fiber.Map {
	return fiber.Map{"message": LocalizedMessage(c, localizer, keyOrMessage)}
}

func LocalizedCoreError(c *fiber.Ctx, localizer MessageLocalizer, keyOrMessage string) core.ErrorResponse {
	return core.Error(LocalizedMessage(c, localizer, keyOrMessage))
}

func RequestLanguage(c *fiber.Ctx) string {
	if language, ok := c.Locals("language").(string); ok && language != "" {
		return NormalizeLanguage(language)
	}

	acceptLanguage := c.Get("Accept-Language")
	if acceptLanguage == "" {
		return DefaultLanguage
	}

	first := strings.Split(acceptLanguage, ",")[0]
	return NormalizeLanguage(first)
}
