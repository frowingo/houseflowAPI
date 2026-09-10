package controllers

import (
	"houseflowApi/external/validator"
	localizationCommands "houseflowApi/internal/application/localization/commands"
	localizationQueries "houseflowApi/internal/application/localization/queries"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/core"
	"houseflowApi/internal/models/dtos"

	"github.com/gofiber/fiber/v2"
)

type LocalizationController struct {
	sender    cqrs.Sender
	localizer helpers.MessageLocalizer
	validator *validator.CustomValidator
}

func NewLocalizationController(sender cqrs.Sender, localizer helpers.MessageLocalizer) *LocalizationController {
	return &LocalizationController{
		sender:    sender,
		localizer: localizer,
		validator: validator.NewValidator(),
	}
}

// @Summary Get plaintext localization values
// @Tags Localization
// @Produce json
// @Param language path string true "Language code"
// @Success 200 {object} core.ApiResponse[[]dtos.LocalizationPlaintextResponseModel]
// @Failure 400 {object} core.ErrorResponse
// @Router /localization/plaintext/{language} [get]
func (r *LocalizationController) GetPlaintexts(c *fiber.Ctx) error {
	language := c.Params("language")

	ctx, cancel := requestContext(c)
	defer cancel()
	plaintexts, err := cqrs.Send[[]dtos.LocalizationPlaintextResponseModel](ctx, r.sender, localizationQueries.GetPlaintextsQuery{
		Language: language,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(core.Success(plaintexts))
}

// @Summary Get supported localization languages
// @Tags Localization
// @Produce json
// @Security BearerAuth
// @Success 200 {object} core.ApiResponse[[]dtos.LocalizationLanguageResponseModel]
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /localization/languages [get]
func (r *LocalizationController) GetLanguages(c *fiber.Ctx) error {
	ctx, cancel := requestContext(c)
	defer cancel()
	languages, err := cqrs.Send[[]dtos.LocalizationLanguageResponseModel](ctx, r.sender, localizationQueries.GetLanguagesQuery{})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(core.Success(languages))
}

// @Summary Get supported localization language by prefix
// @Tags Localization
// @Produce json
// @Security BearerAuth
// @Param prefix path string true "Language prefix"
// @Success 200 {object} core.ApiResponse[[]dtos.LocalizationLanguageResponseModel]
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /localization/language/{prefix} [get]
func (r *LocalizationController) GetLanguage(c *fiber.Ctx) error {
	ctx, cancel := requestContext(c)
	defer cancel()
	languages, err := cqrs.Send[[]dtos.LocalizationLanguageResponseModel](ctx, r.sender, localizationQueries.GetLanguageQuery{
		Prefix: c.Params("prefix"),
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(core.Success(languages))
}

// @Summary Insert localization language
// @Tags Localization
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param language body dtos.LocalizationLanguageRequestModel true "Localization language"
// @Success 201 {object} core.ApiResponse[any]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Forbidden"
// @Router /localization/language [post]
func (r *LocalizationController) InsertLocalizationLanguage(c *fiber.Ctx) error {
	model := new(dtos.LocalizationLanguageRequestModel)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, localizationCommands.InsertLocalizationLanguageCommand{
		Prefix: model.Prefix, Name: model.Name, NativeName: model.NativeName,
		IsDefault: model.IsDefault, IsActive: model.IsActive, Image: model.Image,
	}); err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusCreated).JSON(core.Success[any](nil))
}

// @Summary Insert localization values
// @Tags Localization
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param localization body []dtos.LocalizationRequestModel true "Localization values"
// @Success 201 {object} core.ApiResponse[any]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Forbidden"
// @Router /language [post]
func (r *LocalizationController) InsertLocalizations(c *fiber.Ctx) error {
	var models []dtos.LocalizationRequestModel

	if err := c.BodyParser(&models); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}
	if len(models) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.request_body_required"))
	}

	for _, model := range models {
		if err := r.validator.Validate(model); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
		}
	}
	localizations := make([]localizationCommands.LocalizationValue, 0, len(models))
	for _, model := range models {
		localizations = append(localizations, localizationCommands.LocalizationValue{
			Type: model.Type, Language: model.Language, Key: model.Key, Value: model.Value,
		})
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, localizationCommands.InsertLocalizationsCommand{
		Localizations: localizations,
	}); err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusCreated).JSON(core.Success[any](nil))
}
