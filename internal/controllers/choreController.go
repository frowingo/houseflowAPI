package controllers

import (
	"houseflowApi/external/validator"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/internal/services"

	"github.com/gofiber/fiber/v2"
)

type ChoreController struct {
	choreService *services.ChoreService
	validator    *validator.CustomValidator
}

// NewChoreController constructor for ChoreController
func NewChoreController(choreService *services.ChoreService) *ChoreController {
	return &ChoreController{
		choreService: choreService,
		validator:    validator.NewValidator(),
	}
}

// @Summary Create new chore
// @Description Create a new chore in a house
// @Tags Chore
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param chore body dtos.CreateChoreModel true "Chore object"
// @Success 201 {object} dtos.ChoreResponseModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /chore [post]
func (r *ChoreController) CreateChore(c *fiber.Ctx) error {

	chore := new(dtos.CreateChoreModel)

	if err := c.BodyParser(chore); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// Validate request
	if err := r.validator.Validate(chore); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Get user ID from JWT token
	userId := c.Locals("userID").(string)

	// Create chore
	createdChore, err := r.choreService.CreateChore(*chore, userId)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(createdChore)
}

// @Summary Update chore status
// @Description Update the status of multiple chores
// @Tags Chore
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param statusUpdates body dtos.BulkUpdateChoreStatusModel true "Array of status updates"
// @Success 200 {boolean} bool
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /chore/status [put]
func (r *ChoreController) UpdateChoreStatus(c *fiber.Ctx) error {

	var statusUpdates dtos.BulkUpdateChoreStatusModel

	if err := c.BodyParser(&statusUpdates); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if err := r.validator.Validate(statusUpdates); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Get user ID from JWT token
	userId := c.Locals("userID").(string)

	result, err := r.choreService.UpdateChoreStatusBulk(statusUpdates, userId)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusOK).JSON(result)
}

// @Summary Update chore
// @Description Update an existing chore
// @Tags Chore
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chore ID"
// @Param chore body dtos.CreateChoreModel true "Chore object"
// @Success 200 {object} dtos.ChoreResponseModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Not found"
// @Router /chore/{id} [put]
func (r *ChoreController) UpdateChore(c *fiber.Ctx) error {

	id := c.Params("id")

	chore := new(dtos.CreateChoreModel)

	if err := c.BodyParser(chore); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// Validate request
	if err := r.validator.Validate(chore); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	updatedChore, err := r.choreService.UpdateChore(id, *chore)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusOK).JSON(updatedChore)
}
