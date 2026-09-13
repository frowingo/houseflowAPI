package controllers

import (
	"houseflowApi/external/validator"
	housecommands "houseflowApi/internal/application/house/commands"
	housequeries "houseflowApi/internal/application/house/queries"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/core"
	"houseflowApi/internal/models/dtos"
	"strings"

	"github.com/gofiber/fiber/v2"
)

type HouseController struct {
	sender    cqrs.Sender
	localizer helpers.MessageLocalizer
	validator *validator.CustomValidator
}

func NewHouseController(
	sender cqrs.Sender,
	localizer helpers.MessageLocalizer,
) *HouseController {
	return &HouseController{
		sender:    sender,
		localizer: localizer,
		validator: validator.NewValidator(),
	}
}

// @Summary Create new house
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param house body dtos.CreateHouseModel true "House object"
// @Success 200 {object} core.ApiResponse[dtos.HouseResponseModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /house/create [post]
func (r *HouseController) CreateHouse(c *fiber.Ctx) error {
	model := new(dtos.CreateHouseModel)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}

	model.OwnerId = c.Locals("userID").(string)

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	house, err := cqrs.Send[*entities.House](ctx, r.sender, housecommands.CreateHouseCommand{
		OwnerID:        model.OwnerId,
		Name:           model.Name,
		Type:           model.Type,
		MaxMemberCount: model.MaxMemberCount,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	response := dtos.HouseToResponseModel(*house)
	return c.Status(fiber.StatusOK).JSON(core.Success(response))
}

// @Summary Generate a temporary house invite code
// @Description Generates an 8-character invite code that expires after the configured validity period.
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param inviteRequest body dtos.GenerateHouseInviteCodeModel true "Invite code request"
// @Success 200 {object} core.ApiResponse[dtos.HouseInviteCodeResponseModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /house/inviteCode [post]
func (r *HouseController) GenerateInviteCode(c *fiber.Ctx) error {
	model := new(dtos.GenerateHouseInviteCodeModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	userID := c.Locals("userID").(string)
	ctx, cancel := requestContext(c)
	defer cancel()
	response, err := cqrs.Send[*dtos.HouseInviteCodeResponseModel](ctx, r.sender, housecommands.GenerateHouseInviteCodeCommand{
		HouseID: model.HouseId,
		UserID:  userID,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(core.Success(response))
}

// @Summary Get house details
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param houseId query string true "House ID"
// @Success 200 {object} core.ApiResponse[dtos.HouseDetailsModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /house/details [get]
func (r *HouseController) GetHouseDetails(c *fiber.Ctx) error {
	houseId := c.Query("houseId")
	if houseId == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "house.error.house_id_query_required"))
	}

	userId := c.Locals("userID").(string)

	ctx, cancel := requestContext(c)
	defer cancel()
	details, err := cqrs.Send[*dtos.HouseDetailsModel](ctx, r.sender, housequeries.GetHouseDetailsQuery{
		HouseID:     houseId,
		RequesterID: userId,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(core.Success(details))
}

// @Summary Get house profile information
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param houseId query string true "House ID"
// @Success 200 {object} core.ApiResponse[dtos.HouseInfosResponseModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Requester is not a house member"
// @Failure 404 {object} core.ErrorResponse "House not found"
// @Router /house/infos [get]
func (r *HouseController) GetHouseInfos(c *fiber.Ctx) error {
	houseID := c.Query("houseId")
	if houseID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "house.error.house_id_query_required"))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	response, err := cqrs.Send[*dtos.HouseInfosResponseModel](ctx, r.sender, housequeries.GetHouseInfosQuery{
		HouseID:     houseID,
		RequesterID: c.Locals("userID").(string),
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(core.Success(response))
}

// @Summary Update house profile
// @Description Only the house owner can update fields. Each field can be changed once every 48 hours.
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param houseId query string true "House ID"
// @Param profile body dtos.UpdateHouseProfileModel true "House profile fields"
// @Success 200 {object} core.ApiResponse[dtos.HouseInfosResponseModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Only the house owner can update the profile"
// @Failure 404 {object} core.ErrorResponse "House not found"
// @Failure 409 {object} core.ErrorResponse "Member limit is below current member count"
// @Failure 429 {object} core.ErrorResponse "Field update interval has not elapsed"
// @Router /house/profile [put]
func (r *HouseController) UpdateHouseProfile(c *fiber.Ctx) error {
	houseID := c.Query("houseId")
	if houseID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "house.error.house_id_query_required"))
	}

	model := new(dtos.UpdateHouseProfileModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}
	if !model.HasChanges() {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "house.error.profile_no_fields"))
	}
	if model.HouseName != nil {
		value := strings.TrimSpace(*model.HouseName)
		model.HouseName = &value
	}
	if model.HouseProfileImage != nil {
		value := strings.TrimSpace(*model.HouseProfileImage)
		model.HouseProfileImage = &value
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	response, err := cqrs.Send[*dtos.HouseInfosResponseModel](ctx, r.sender, housecommands.UpdateHouseProfileCommand{
		HouseID:               houseID,
		RequesterID:           c.Locals("userID").(string),
		HouseName:             model.HouseName,
		HouseProfileImage:     model.HouseProfileImage,
		HouseMemberCountLimit: model.HouseMemberCountLimit,
		HouseType:             model.HouseType,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(core.Success(response))
}

// @Summary Exit a house or remove a member
// @Description A member can leave the house; the owner can remove another member.
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param exitRequest body dtos.ExitHouseModel true "House and target user"
// @Success 200 {object} dtos.SuccessResponseModel
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Insufficient house permissions"
// @Failure 404 {object} core.ErrorResponse "House or member not found"
// @Failure 409 {object} core.ErrorResponse "House membership changed concurrently"
// @Router /house/exit [post]
func (r *HouseController) ExitHouse(c *fiber.Ctx) error {
	model := new(dtos.ExitHouseModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	if _, err := cqrs.Send[cqrs.NoResult](ctx, r.sender, housecommands.ExitHouseCommand{
		HouseID:      model.HouseId,
		TargetUserID: model.UserId,
		RequesterID:  c.Locals("userID").(string),
	}); err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(dtos.SuccessResponseModel{Success: true})
}

// @Summary Create a house announcement
// @Description Create an announcement that is displayed for 24 hours
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param announcement body dtos.CreateAnnouncementModel true "Announcement object"
// @Success 200 {object} core.ApiResponse[dtos.AnnouncementResponseModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /house/announcement [post]
func (r *HouseController) CreateAnnouncement(c *fiber.Ctx) error {
	model := new(dtos.CreateAnnouncementModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.invalid_request_body"))
	}
	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	userId := c.Locals("userID").(string)
	ctx, cancel := requestContext(c)
	defer cancel()
	announcement, err := cqrs.Send[*dtos.AnnouncementResponseModel](ctx, r.sender, housecommands.CreateAnnouncementCommand{
		HouseID:     model.HouseId,
		UserID:      userId,
		Title:       model.Title,
		Description: model.Description,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	return c.Status(fiber.StatusOK).JSON(core.Success(announcement))
}

// @Summary Join house by invite code
// @Tags House
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param joinRequest body dtos.JoinHouseByCodeModel true "Join request"
// @Success 200 {object} core.ApiResponse[dtos.HouseResponseModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Router /house/join [post]
func (r *HouseController) JoinHouseByCode(c *fiber.Ctx) error {
	model := new(dtos.JoinHouseByCodeModel)

	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, "common.error.cannot_parse_json"))
	}
	model.InviteCode = helpers.NormalizeInviteCode(model.InviteCode)

	if err := r.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(helpers.LocalizedCoreError(c, r.localizer, err.Error()))
	}

	userID := c.Locals("userID").(string)

	ctx, cancel := requestContext(c)
	defer cancel()
	house, err := cqrs.Send[*entities.House](ctx, r.sender, housecommands.JoinHouseCommand{
		UserID:     userID,
		InviteCode: model.InviteCode,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, r.localizer, err)
	}

	response := dtos.HouseToResponseModel(*house)
	return c.Status(fiber.StatusOK).JSON(core.Success(response))
}
