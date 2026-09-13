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

type ErrorKind uint8

const (
	ErrorKindBadRequest ErrorKind = iota
	ErrorKindNotFound
	ErrorKindForbidden
	ErrorKindConflict
	ErrorKindRateLimited
	ErrorKindUnavailable
)

type ApplicationError struct {
	Kind  ErrorKind
	Key   string
	Args  []string
	Cause error
}

func (e *ApplicationError) Error() string {
	return LocalizationMessage(e.Key, e.Args...)
}

func (e *ApplicationError) Unwrap() error {
	return e.Cause
}

func NewLocalizedError(key string, args ...string) error {
	return newApplicationError(ErrorKindBadRequest, key, nil, args...)
}

func NewNotFoundError(key string, args ...string) error {
	return newApplicationError(ErrorKindNotFound, key, nil, args...)
}

func NewForbiddenError(key string, args ...string) error {
	return newApplicationError(ErrorKindForbidden, key, nil, args...)
}

func NewConflictError(key string, args ...string) error {
	return newApplicationError(ErrorKindConflict, key, nil, args...)
}

func NewRateLimitError(key string, args ...string) error {
	return newApplicationError(ErrorKindRateLimited, key, nil, args...)
}

func NewUnavailableError(key string, cause error, args ...string) error {
	return newApplicationError(ErrorKindUnavailable, key, cause, args...)
}

func newApplicationError(kind ErrorKind, key string, cause error, args ...string) error {
	return &ApplicationError{
		Kind:  kind,
		Key:   key,
		Args:  args,
		Cause: cause,
	}
}

func IsApplicationError(err error, key string) bool {
	var applicationError *ApplicationError
	return errors.As(err, &applicationError) && applicationError.Key == key
}

func SplitLocalizationMessage(value string) (string, []string) {
	parts := strings.Split(value, localizationArgSeparator)
	if len(parts) == 0 {
		return value, nil
	}
	return parts[0], parts[1:]
}
