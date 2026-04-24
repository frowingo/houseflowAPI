package controllers

import (
	"houseflowApi/external/validator"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/internal/services"

	"github.com/gofiber/fiber/v2"
)

type HouseController struct {
	houseService *services.HouseService
	validator    *validator.CustomValidator
}

func NewHouseController(houseService *services.HouseService) *HouseController {
	return &HouseController{
		houseService: houseService,
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Cannot parse JSON",
		})
	}

	model.OwnerId = c.Locals("userID").(string)

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	house, err := r.houseService.CreateHouse(*model)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "houseId query param is required",
		})
	}

	details, err := r.houseService.GetHouseDetails(houseId)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusOK).JSON(details)
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Cannot parse JSON",
		})
	}

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	house, err := r.houseService.JoinHouseByCode(*model)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	response := dtos.HouseToResponseModel(*house)
	return c.Status(fiber.StatusOK).JSON(response)
}
