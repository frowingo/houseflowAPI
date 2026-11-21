package notification

import (
	"context"
	"strings"

	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
)

// Render returns the title and message of a notification in a language.
// The panel and push notifications use the same text.
func Render(ctx context.Context, localizer helpers.MessageLocalizer, language string, notification entities.Notification) (string, string) {
	if !notification.Localized || localizer == nil {
		return notification.Title, notification.Message
	}
	return localizer.LocalizeMessage(ctx, language, helpers.LocalizationMessage(notification.Title, notification.Args...)),
		localizer.LocalizeMessage(ctx, language, helpers.LocalizationMessage(notification.Message, notification.Args...))
}

// argReplacer keeps user text such as a chore title from being read by the
// localizer as an argument separator ("||") or a message list ("; ").
var argReplacer = strings.NewReplacer("||", "|", "; ", "; ")

func SanitizeArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	sanitized := make([]string, len(args))
	for index, arg := range args {
		sanitized[index] = argReplacer.Replace(arg)
	}
	return sanitized
}
