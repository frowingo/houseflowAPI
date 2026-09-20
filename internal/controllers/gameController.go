package controllers

import (
	"houseflowApi/external/validator"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/core"
	"houseflowApi/internal/models/dtos"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type GameController struct {
	sender    cqrs.Sender
	localizer helpers.MessageLocalizer
	validator *validator.CustomValidator
}

func NewGameController(sender cqrs.Sender, localizer helpers.MessageLocalizer) *GameController {
	return &GameController{
		sender:    sender,
		localizer: localizer,
		validator: validator.NewValidator(),
	}
}

// @Summary Ensure an active game session
// @Description Returns the current active session for the house and game, or atomically creates one from the server-side game catalog.
// @Tags Game
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param gameKey path string true "Game key" example(flappyBird)
// @Param request body dtos.EnsureGameSessionModel true "House selection"
// @Success 200 {object} core.ApiResponse[dtos.GameSessionResponseModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Requester is not a house member"
// @Failure 404 {object} core.ErrorResponse "Game not found"
// @Failure 409 {object} core.ErrorResponse "House capacity is too small"
// @Router /game/{gameKey}/session [put]
func (controller *GameController) EnsureActiveSession(c *fiber.Ctx) error {
	model := new(dtos.EnsureGameSessionModel)
	if err := c.BodyParser(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(
			helpers.LocalizedCoreError(c, controller.localizer, "common.error.cannot_parse_json"),
		)
	}
	if err := controller.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(
			helpers.LocalizedCoreError(c, controller.localizer, err.Error()),
		)
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	snapshot, err := cqrs.Send[gameDomain.SessionSnapshot](ctx, controller.sender, gameCommands.EnsureActiveGameSessionCommand{
		CommandID: "ensureActive:" + uuid.NewString(),
		UserID:    c.Locals("userID").(string),
		HouseID:   model.HouseId,
		GameKey:   c.Params("gameKey"),
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, controller.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(core.Success(dtos.GameSessionToResponseModel(snapshot)))
}

// @Summary Get the active game session
// @Description Returns the current non-terminal session for the selected house and game without creating one.
// @Tags Game
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param gameKey path string true "Game key" example(flappyBird)
// @Param houseId query string true "House ID"
// @Success 200 {object} core.ApiResponse[dtos.GameSessionResponseModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Requester is not a house member"
// @Failure 404 {object} core.ErrorResponse "Active session or game not found"
// @Router /game/{gameKey}/session [get]
func (controller *GameController) GetActiveSession(c *fiber.Ctx) error {
	model := dtos.EnsureGameSessionModel{HouseId: c.Query("houseId")}
	if err := controller.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(
			helpers.LocalizedCoreError(c, controller.localizer, err.Error()),
		)
	}

	ctx, cancel := requestContext(c)
	defer cancel()
	snapshot, err := cqrs.Send[gameDomain.SessionSnapshot](ctx, controller.sender, gameQueries.GetActiveGameSessionQuery{
		HouseID: model.HouseId,
		GameKey: c.Params("gameKey"),
		UserID:  c.Locals("userID").(string),
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, controller.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(core.Success(dtos.GameSessionToResponseModel(snapshot)))
}
