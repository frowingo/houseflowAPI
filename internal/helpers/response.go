package helpers

import (
	"context"
	"errors"
	"fmt"
	"houseflowApi/internal/models/core"
	"strings"

	"github.com/gofiber/fiber/v2"
)

type MessageLocalizer interface {
	LocalizeMessage(ctx context.Context, language string, keyOrMessage string) string
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
	return localizer.LocalizeMessage(c.UserContext(), language, keyOrMessage)
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

func RespondLocalizedError(c *fiber.Ctx, localizer MessageLocalizer, err error) error {
	status := fiber.StatusInternalServerError
	message := "common.error.internal_server_error"
	var applicationError *ApplicationError
	if errors.As(err, &applicationError) {
		message = err.Error()
		switch applicationError.Kind {
		case ErrorKindBadRequest:
			status = fiber.StatusBadRequest
		case ErrorKindNotFound:
			status = fiber.StatusNotFound
		case ErrorKindForbidden:
			status = fiber.StatusForbidden
		case ErrorKindConflict:
			status = fiber.StatusConflict
		case ErrorKindRateLimited:
			status = fiber.StatusTooManyRequests
		case ErrorKindUnavailable:
			status = fiber.StatusServiceUnavailable
		}
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = fiber.StatusServiceUnavailable
		message = "database.error.transaction_unavailable"
	}

	return c.Status(status).JSON(LocalizedCoreError(c, localizer, message))
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
