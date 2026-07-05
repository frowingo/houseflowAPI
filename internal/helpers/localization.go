package helpers

import (
	"errors"
	"houseflowApi/internal/data/entities"
	"strings"
)

const (
	DefaultLanguage = string(entities.English)

	localizationArgSeparator = "||"
)

func NormalizeLanguage(language string) string {
	value := strings.ToLower(strings.TrimSpace(language))
	switch {
	case value == "":
		return DefaultLanguage
	case value == "eng", strings.HasPrefix(value, "en"):
		return string(entities.English)
	case strings.HasPrefix(value, "tr"):
		return string(entities.Turkish)
	default:
		return value
	}
}

func IsSupportedLanguage(language string) bool {
	switch NormalizeLanguage(language) {
	case string(entities.English), string(entities.Turkish):
		return true
	default:
		return false
	}
}

func NormalizeLocalizationType(localizationType string) string {
	return strings.ToLower(strings.TrimSpace(localizationType))
}

func IsSupportedLocalizationType(localizationType string) bool {
	switch NormalizeLocalizationType(localizationType) {
	case string(entities.Plaintext), string(entities.Message):
		return true
	default:
		return false
	}
}

func LocalizationMessage(key string, args ...string) string {
	parts := append([]string{key}, args...)
	return strings.Join(parts, localizationArgSeparator)
}

func NewLocalizedError(key string, args ...string) error {
	return errors.New(LocalizationMessage(key, args...))
}

func SplitLocalizationMessage(value string) (string, []string) {
	parts := strings.Split(value, localizationArgSeparator)
	if len(parts) == 0 {
		return value, nil
	}
	return parts[0], parts[1:]
}
