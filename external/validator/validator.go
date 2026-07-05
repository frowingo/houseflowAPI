package validator

import (
	"fmt"
	"houseflowApi/internal/helpers"
	"strings"

	"github.com/go-playground/validator/v10"
)

type CustomValidator struct {
	validator *validator.Validate
}

func NewValidator() *CustomValidator {
	return &CustomValidator{
		validator: validator.New(),
	}
}

func (cv *CustomValidator) Validate(data interface{}) error {
	if err := cv.validator.Struct(data); err != nil {
		return cv.formatValidationErrors(err)
	}
	return nil
}

func (cv *CustomValidator) formatValidationErrors(err error) error {
	if validationErrors, ok := err.(validator.ValidationErrors); ok {
		var messages []string
		for _, e := range validationErrors {
			messages = append(messages, cv.getErrorMessage(e))
		}
		return fmt.Errorf("%s", strings.Join(messages, "; "))
	}
	return err
}

func (cv *CustomValidator) getErrorMessage(e validator.FieldError) string {
	field := e.Field()

	switch e.Tag() {
	case "required":
		return helpers.LocalizationMessage("validation.error.required", field)
	case "email":
		return helpers.LocalizationMessage("validation.error.email", field)
	case "min":
		return helpers.LocalizationMessage("validation.error.min", field, e.Param())
	case "max":
		return helpers.LocalizationMessage("validation.error.max", field, e.Param())
	case "len":
		return helpers.LocalizationMessage("validation.error.len", field, e.Param())
	case "oneof":
		return helpers.LocalizationMessage("validation.error.oneof", field, e.Param())
	case "gte":
		return helpers.LocalizationMessage("validation.error.gte", field, e.Param())
	case "lte":
		return helpers.LocalizationMessage("validation.error.lte", field, e.Param())
	case "gt":
		return helpers.LocalizationMessage("validation.error.gt", field, e.Param())
	case "lt":
		return helpers.LocalizationMessage("validation.error.lt", field, e.Param())
	case "alphanum":
		return helpers.LocalizationMessage("validation.error.alphanum", field)
	case "numeric":
		return helpers.LocalizationMessage("validation.error.numeric", field)
	case "url":
		return helpers.LocalizationMessage("validation.error.url", field)
	default:
		return helpers.LocalizationMessage("validation.error.failed", field, e.Tag())
	}
}

// for validate single variable with tag
func (cv *CustomValidator) ValidateVar(field interface{}, tag string) error {
	if err := cv.validator.Var(field, tag); err != nil {
		return cv.formatValidationErrors(err)
	}
	return nil
}
