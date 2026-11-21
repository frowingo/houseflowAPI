package controllers

import (
	"houseflowApi/external/validator"
	notificationCommands "houseflowApi/internal/application/notification/commands"
	notificationQueries "houseflowApi/internal/application/notification/queries"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/core"
	"houseflowApi/internal/models/dtos"

	"github.com/gofiber/fiber/v2"
)

type NotificationController struct {
	sender    cqrs.Sender
	localizer helpers.MessageLocalizer
	validator *validator.CustomValidator
}

func NewNotificationController(sender cqrs.Sender, localizer helpers.MessageLocalizer) *NotificationController {
	return &NotificationController{
		sender:    sender,
		localizer: localizer,
		validator: validator.NewValidator(),
	}
}

// @Summary List house notifications
// @Description Lists the notifications of a house the requester can see, newest first, in the request language. Pass nextCursor as before to load older ones. Read state is kept by the client.
// @Tags Notification
// @Produce json
// @Security BearerAuth
// @Param houseId query string true "House ID"
// @Param before query string false "Cursor from the previous page"
// @Param limit query int false "Page size, at most 50 (default 20)"
// @Success 200 {object} core.ApiResponse[dtos.NotificationPageModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Requester is not a house member"
// @Router /notification [get]
func (r *NotificationController) GetNotifications(c *fiber.Ctx) error {
	houseID := c.Query("houseId")
	if houseID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "house.error.house_id_query_required"))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	page, err := cqrs.Send[*dtos.NotificationPageModel](ctx, r.sender, notificationQueries.GetNotificationsQuery{
		HouseID:     houseID,
		RequesterID: c.Locals("userID").(string),
		Language:    helpers.RequestLanguage(c),
		Before:      c.Query("before"),
		Limit:       c.QueryInt("limit"),
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(core.Success(page))
}

// @Summary Register a device for push notifications
// @Description Stores the FCM registration token of the signed-in device. Call it after sign-in and whenever the token changes.
// @Tags Notification
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param device body dtos.RegisterDeviceModel true "Device"
// @Success 200 {object} dtos.SuccessResponseModel
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /notification/devices [post]
func (r *NotificationController) RegisterDevice(c *fiber.Ctx) error {
	model := new(dtos.RegisterDeviceModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, notificationCommands.RegisterDeviceCommand{
		UserID:   c.Locals("userID").(string),
		Token:    model.Token,
		Platform: model.Platform,
	}); err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(dtos.SuccessResponseModel{Success: true})
}

// @Summary Remove a device from push notifications
// @Description Removes the FCM registration token of the signed-in device. Call it on sign-out.
// @Tags Notification
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param device body dtos.DeleteDeviceModel true "Device"
// @Success 200 {object} dtos.SuccessResponseModel
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /notification/devices [delete]
func (r *NotificationController) DeleteDevice(c *fiber.Ctx) error {
	model := new(dtos.DeleteDeviceModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, notificationCommands.DeleteDeviceCommand{
		UserID: c.Locals("userID").(string),
		Token:  model.Token,
	}); err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(dtos.SuccessResponseModel{Success: true})
}
