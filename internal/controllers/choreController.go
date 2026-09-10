package controllers

import (
	"houseflowApi/external/validator"
	choreCommands "houseflowApi/internal/application/chore/commands"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"github.com/gofiber/fiber/v2"
)

type ChoreController struct {
	sender    cqrs.Sender
	localizer helpers.MessageLocalizer
	validator *validator.CustomValidator
}

// NewChoreController constructor for ChoreController
func NewChoreController(sender cqrs.Sender, localizer helpers.MessageLocalizer) *ChoreController {
	return &ChoreController{
		sender:    sender,
		localizer: localizer,
		validator: validator.NewValidator(),
	}
}

// @Summary Create new chore
// @Description Create a new chore in a house
// @Tags Chore
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param chore body dtos.CreateChoreModel true "Chore object"
// @Success 200 {object} dtos.ChoreResponseModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /chore [post]
func (r *ChoreController) CreateChore(c *fiber.Ctx) error {

	chore := new(dtos.CreateChoreModel)

	if err := c.BodyParser(chore); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.invalid_request_body"))
	}

	if err := r.validator.Validate(chore); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	userId := c.Locals("userID").(string)

	ctx, cancel := requestContext(c)
	defer cancel()
	createdChore, err := cqrs.Send[*dtos.ChoreResponseModel](ctx, r.sender, choreCommands.CreateChoreCommand{
		Title:             chore.Title,
		Description:       chore.Description,
		AssignedTo:        chore.AssignedTo,
		DueDate:           chore.DueDate.Time,
		HouseID:           chore.HouseId,
		Level:             chore.Level,
		IsRecurring:       chore.IsRecurring,
		RecurringInterval: chore.RecurringInterval,
		RequesterID:       userId,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(createdChore)
}

// @Summary Update chore status
// @Description Update the status of multiple chores
// @Tags Chore
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param statusUpdates body dtos.BulkUpdateChoreStatusModel true "Array of status updates"
// @Success 200 {array} dtos.ChoreResponseModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /chore/status [put]
func (r *ChoreController) UpdateChoreStatus(c *fiber.Ctx) error {

	var statusUpdates dtos.BulkUpdateChoreStatusModel

	if err := c.BodyParser(&statusUpdates); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.invalid_request_body"))
	}

	if err := r.validator.Validate(statusUpdates); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	userId := c.Locals("userID").(string)

	ctx, cancel := requestContext(c)
	defer cancel()
	updates := make([]choreCommands.ChoreStatusUpdate, 0, len(statusUpdates.Chores))
	for _, update := range statusUpdates.Chores {
		updates = append(updates, choreCommands.ChoreStatusUpdate{
			ChoreID: update.ChoreId,
			Status:  update.Status,
		})
	}
	result, err := cqrs.Send[[]dtos.ChoreResponseModel](ctx, r.sender, choreCommands.UpdateChoreStatusCommand{
		HouseID: statusUpdates.HouseId,
		Chores:  updates,
		UserID:  userId,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(result)
}

// @Summary Review chore
// @Description Approve or reject a chore that is in review
// @Tags Chore
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param review body dtos.ReviewChoreModel true "Review object"
// @Success 200 {object} dtos.ChoreResponseModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /chore/review [put]
func (r *ChoreController) ReviewChore(c *fiber.Ctx) error {

	review := new(dtos.ReviewChoreModel)

	if err := c.BodyParser(review); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.invalid_request_body"))
	}

	if err := r.validator.Validate(review); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	userId := c.Locals("userID").(string)

	ctx, cancel := requestContext(c)
	defer cancel()
	result, err := cqrs.Send[*dtos.ChoreResponseModel](ctx, r.sender, choreCommands.ReviewChoreCommand{
		ChoreID:    review.ChoreId,
		ReviewerID: userId,
		IsApproved: *review.IsApproved,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
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
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.invalid_request_body"))
	}

	if err := r.validator.Validate(chore); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}

	userId := c.Locals("userID").(string)

	ctx, cancel := requestContext(c)
	defer cancel()
	updatedChore, err := cqrs.Send[*dtos.ChoreResponseModel](ctx, r.sender, choreCommands.UpdateChoreCommand{
		ChoreID:           id,
		Title:             chore.Title,
		Description:       chore.Description,
		AssignedTo:        chore.AssignedTo,
		DueDate:           chore.DueDate.Time,
		HouseID:           chore.HouseId,
		Level:             chore.Level,
		IsRecurring:       chore.IsRecurring,
		RecurringInterval: chore.RecurringInterval,
		RequesterID:       userId,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(updatedChore)
}
