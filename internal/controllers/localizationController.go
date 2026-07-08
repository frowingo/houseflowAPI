package controllers

import (
	"houseflowApi/external/validator"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/core"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/internal/services"

	"github.com/gofiber/fiber/v2"
)

type LocalizationController struct {
	localizationService *services.LocalizationService
	validator           *validator.CustomValidator
}

func NewLocalizationController(localizationService *services.LocalizationService) *LocalizationController {
	return &LocalizationController{
		localizationService: localizationService,
		validator:           validator.NewValidator(),
	}
}

// @Summary Get plaintext localization values
// @Tags Localization
// @Produce json
// @Param language path string true "Language code"
// @Success 200 {object} core.ApiResponse[[]dtos.LocalizationPlaintextResponseModel]
// @Failure 400 {object} core.ErrorResponse
// @Router /localization/plaintext [get]
// @Router /localization/plaintexts/{language} [get]
func (r *LocalizationController) GetPlaintexts(c *fiber.Ctx) error {
	language := c.Params("language")
	if language == "" {
		language = c.Query("language")
	}

	plaintexts, err := r.localizationService.GetPlaintexts(language)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizationService, err.Error()))
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
	languages, err := r.localizationService.GetLanguages()
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizationService, err.Error()))
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
	languages, err := r.localizationService.GetLanguage(c.Params("prefix"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizationService, err.Error()))
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
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizationService, "common.error.cannot_parse_json"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizationService, err.Error()))
	}

	if err := r.localizationService.InsertLocalizationLanguage(*model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizationService, err.Error()))
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
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizationService, "common.error.cannot_parse_json"))
	}
	if len(models) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizationService, "common.error.request_body_required"))
	}

	for _, model := range models {
		if err := r.validator.Validate(model); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizationService, err.Error()))
		}
	}

	if err := r.localizationService.InsertLocalizations(models); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizationService, err.Error()))
	}

	return c.Status(fiber.StatusCreated).JSON(core.Success[any](nil))
}
