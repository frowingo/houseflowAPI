package controllers

import (
	"houseflowApi/external/validator"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/internal/services"

	"github.com/gofiber/fiber/v2"
)

type HouseController struct {
	houseService *services.HouseService
	localizer    helpers.MessageLocalizer
	validator    *validator.CustomValidator
}

func NewHouseController(houseService *services.HouseService, localizers ...helpers.MessageLocalizer) *HouseController {
	var localizer helpers.MessageLocalizer
	if len(localizers) > 0 {
		localizer = localizers[0]
	}
	return &HouseController{
		houseService: houseService,
		localizer:    localizer,
		validator:    validator.NewValidator(),
	}
}

// @Summary Create new house
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param house body dtos.CreateHouseModel true "House object"
// @Success 200 {object} dtos.HouseResponseModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /house/create [post]
func (r *HouseController) CreateHouse(c *fiber.Ctx) error {
	model := new(dtos.CreateHouseModel)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.cannot_parse_json"))
	}

	model.OwnerId = c.Locals("userID").(string)

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	house, err := r.houseService.CreateHouse(*model)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	response := dtos.HouseToResponseModel(*house)
	return c.Status(fiber.StatusOK).JSON(response)
}

// @Summary Get house details
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param houseId query string true "House ID"
// @Success 200 {object} dtos.HouseDetailsModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /house/details [get]
func (r *HouseController) GetHouseDetails(c *fiber.Ctx) error {
	houseId := c.Query("houseId")
	if houseId == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "house.error.house_id_query_required"))
	}

	userId := c.Locals("userID").(string)

	details, err := r.houseService.GetHouseDetails(houseId, userId)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	return c.Status(fiber.StatusOK).JSON(details)
}

// @Summary Create a house announcement
// @Description Create an announcement that is displayed for 24 hours
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param announcement body dtos.CreateAnnouncementModel true "Announcement object"
// @Success 200 {object} dtos.AnnouncementResponseModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /house/announcement [post]
func (r *HouseController) CreateAnnouncement(c *fiber.Ctx) error {
	model := new(dtos.CreateAnnouncementModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.invalid_request_body"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	userId := c.Locals("userID").(string)
	announcement, err := r.houseService.CreateAnnouncement(*model, userId)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	return c.Status(fiber.StatusOK).JSON(announcement)
}

// @Summary Join house by invite code
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param joinRequest body dtos.JoinHouseByCodeModel true "Join request"
// @Success 200 {object} dtos.HouseResponseModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /house/join [post]
func (r *HouseController) JoinHouseByCode(c *fiber.Ctx) error {
	model := new(dtos.JoinHouseByCodeModel)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.cannot_parse_json"))
	}

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	model.UserId = c.Locals("userID").(string)

	house, err := r.houseService.JoinHouseByCode(*model)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	response := dtos.HouseToResponseModel(*house)
	return c.Status(fiber.StatusOK).JSON(response)
}
