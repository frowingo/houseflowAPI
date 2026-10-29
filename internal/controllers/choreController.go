package controllers

import (
	"encoding/json"
	"errors"
	"mime/multipart"
	"strconv"
	"strings"

	"houseflowApi/external/validator"
	choreCommands "houseflowApi/internal/application/chore/commands"
	choreQueries "houseflowApi/internal/application/chore/queries"
	"houseflowApi/internal/data/entities"
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
// @Description Update statuses with JSON. Multipart requests use houseId, a JSON-encoded chores field, and an optional file; they must contain one InTest transition.
// @Tags Chore
// @Accept json,mpfd
// @Produce json
// @Security BearerAuth
// @Param statusUpdates body dtos.BulkUpdateChoreStatusModel true "Array of status updates"
// @Success 200 {array} dtos.ChoreResponseModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /chore/status [put]
func (r *ChoreController) UpdateChoreStatus(c *fiber.Ctx) error {
	isMultipart := strings.HasPrefix(strings.ToLower(c.Get(fiber.HeaderContentType)), "multipart/form-data")
	statusUpdates, photoHeader, err := parseChoreStatusRequest(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, "common.error.invalid_request_body"))
	}

	if err := r.validator.Validate(statusUpdates); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedErrorMap(c, r.localizer, err.Error()))
	}
	if isMultipart && (len(statusUpdates.Chores) != 1 || statusUpdates.Chores[0].Status != entities.InTest) {
		return helpers.RespondLocalizedError(c, r.localizer,
			helpers.NewLocalizedError("chore.error.review_photo_single_transition_required"))
	}

	var photoFile multipart.File
	if photoHeader != nil {
		photoFile, err = photoHeader.Open()
		if err != nil {
			return helpers.RespondLocalizedError(c, r.localizer,
				helpers.NewLocalizedError("chore.error.review_photo_invalid"))
		}
		defer photoFile.Close()
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
		Photo:   choreReviewPhoto(photoFile, photoHeader),
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(result)
}

func parseChoreStatusRequest(c *fiber.Ctx) (dtos.BulkUpdateChoreStatusModel, *multipart.FileHeader, error) {
	var model dtos.BulkUpdateChoreStatusModel
	contentType := strings.ToLower(c.Get(fiber.HeaderContentType))
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		return model, nil, c.BodyParser(&model)
	}

	form, err := c.MultipartForm()
	if err != nil {
		return model, nil, err
	}
	model.HouseId = firstMultipartValue(form.Value["houseId"])
	choresJSON := firstMultipartValue(form.Value["chores"])
	if choresJSON == "" || json.Unmarshal([]byte(choresJSON), &model.Chores) != nil {
		return model, nil, errors.New("invalid multipart chores")
	}

	files := form.File["file"]
	if len(files) > 1 {
		return model, nil, errors.New("only one review photo is allowed")
	}
	if len(files) == 1 {
		return model, files[0], nil
	}
	return model, nil, nil
}

func firstMultipartValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func choreReviewPhoto(file multipart.File, header *multipart.FileHeader) *choreCommands.ChoreReviewPhoto {
	if file == nil || header == nil {
		return nil
	}
	return &choreCommands.ChoreReviewPhoto{Reader: file, Size: header.Size}
}

// @Summary Get a temporary chore review photo URL
// @Description Returns a five-minute presigned URL after verifying house membership.
// @Tags Chore
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chore ID"
// @Param round path int true "Review round"
// @Success 200 {object} dtos.ChoreReviewPhotoURLModel
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not a house member"
// @Failure 404 {object} map[string]interface{} "Photo not found"
// @Router /chore/{id}/review-photo/{round} [get]
func (r *ChoreController) GetReviewPhotoURL(c *fiber.Ctx) error {
	round, err := strconv.Atoi(c.Params("round"))
	if err != nil || round <= 0 {
		return helpers.RespondLocalizedError(c, r.localizer,
			helpers.NewLocalizedError("chore.error.invalid_review_round"))
	}

	userID := c.Locals("userID").(string)
	ctx, cancel := requestContext(c)
	defer cancel()
	result, err := cqrs.Send[*dtos.ChoreReviewPhotoURLModel](ctx, r.sender, choreQueries.GetReviewPhotoURLQuery{
		ChoreID:     c.Params("id"),
		Round:       round,
		RequesterID: userID,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}
	c.Set(fiber.HeaderCacheControl, "private, no-store")
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
