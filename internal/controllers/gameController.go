package controllers

import (
	"houseflowApi/external/validator"
	gameApplication "houseflowApi/internal/application/game"
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

// @Summary Get the immutable match result
// @Description Returns the committed result in its game's own wire model.
// @Tags Game
// @Produce json
// @Security BearerAuth
// @Param sessionId path string true "Game session ID"
// @Success 200 {object} core.ApiResponse[any]
// @Failure 401 {object} core.ErrorResponse
// @Failure 403 {object} core.ErrorResponse "Current house membership required"
// @Failure 404 {object} core.ErrorResponse "Session or committed result not found"
// @Router /game/sessions/{sessionId}/result [get]
func (controller *GameController) GetResult(c *fiber.Ctx) error {
	ctx, cancel := requestContext(c)
	defer cancel()
	result, err := cqrs.Send[any](ctx, controller.sender, gameQueries.GetMatchResultQuery{SessionID: c.Params("sessionId"), UserID: c.Locals("userID").(string)})
	if err != nil {
		return helpers.RespondLocalizedError(c, controller.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(core.Success(result))
}

// @Summary Ensure an active game session
// @Description Returns the current active session for the house and game, or atomically creates one from the server-side game catalog.
// @Tags Game
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param gameKey path string true "Game key" example(houseRockets)
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
// @Param gameKey path string true "Game key" example(houseRockets)
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

// @Summary List the games a house can start
// @Description Returns enabled games available to the house with the rules (capped by house capacity) and client settings a new session would get. Running sessions keep the config they were created with.
// @Tags Game
// @Produce json
// @Security BearerAuth
// @Param houseId query string true "House ID"
// @Success 200 {object} core.ApiResponse[[]dtos.GameCatalogEntryModel]
// @Failure 400 {object} core.ErrorResponse
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Requester is not a house member"
// @Router /game/catalog [get]
func (controller *GameController) GetCatalog(c *fiber.Ctx) error {
	model := dtos.EnsureGameSessionModel{HouseId: c.Query("houseId")}
	if err := controller.validator.Validate(model); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(
			helpers.LocalizedCoreError(c, controller.localizer, err.Error()),
		)
	}
	ctx, cancel := requestContext(c)
	defer cancel()
	entries, err := cqrs.Send[[]gameApplication.CatalogEntry](ctx, controller.sender, gameQueries.GetGameCatalogQuery{HouseID: model.HouseId, UserID: c.Locals("userID").(string)})
	if err != nil {
		return helpers.RespondLocalizedError(c, controller.localizer, err)
	}
	models := make([]dtos.GameCatalogEntryModel, 0, len(entries))
	for _, entry := range entries {
		models = append(models, dtos.GameCatalogEntryToModel(entry.Definition, entry.Config, entry.ClientSettings))
	}
	return c.Status(fiber.StatusOK).JSON(core.Success(models))
}

// @Summary List game configs
// @Description Returns every game this deployment can run with its current config, including switched-off games.
// @Tags Game
// @Produce json
// @Security BearerAuth
// @Success 200 {object} core.ApiResponse[[]dtos.GameConfigModel]
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Super admin role required"
// @Router /game/configs [get]
func (controller *GameController) GetConfigs(c *fiber.Ctx) error {
	ctx, cancel := requestContext(c)
	defer cancel()
	entries, err := cqrs.Send[[]gameApplication.CatalogEntry](ctx, controller.sender, gameQueries.GetGameConfigsQuery{})
	if err != nil {
		return helpers.RespondLocalizedError(c, controller.localizer, err)
	}
	models := make([]dtos.GameConfigModel, 0, len(entries))
	for _, entry := range entries {
		models = append(models, dtos.GameConfigToModel(entry.Definition, entry.Config))
	}
	return c.Status(fiber.StatusOK).JSON(core.Success(models))
}

// @Summary Replace a game config
// @Description Switches a game on or off, limits it to listed houses and changes its rules and settings without a deploy. All instances apply it within seconds; existing sessions keep the config they were created with.
// @Tags Game
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param gameKey path string true "Game key" example(houseRockets)
// @Param request body dtos.UpdateGameConfigModel true "New config"
// @Success 200 {object} core.ApiResponse[dtos.GameConfigModel]
// @Failure 400 {object} core.ErrorResponse "Invalid config"
// @Failure 401 {object} core.ErrorResponse "Unauthorized"
// @Failure 403 {object} core.ErrorResponse "Super admin role required"
// @Failure 404 {object} core.ErrorResponse "Game not found"
// @Failure 409 {object} core.ErrorResponse "Config changed since expectedVersion"
// @Router /game/{gameKey}/config [put]
func (controller *GameController) UpdateConfig(c *fiber.Ctx) error {
	model := new(dtos.UpdateGameConfigModel)
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
	entry, err := cqrs.Send[gameApplication.CatalogEntry](ctx, controller.sender, gameCommands.UpdateGameConfigCommand{
		GameKey:         c.Params("gameKey"),
		UserID:          c.Locals("userID").(string),
		ExpectedVersion: model.ExpectedVersion,
		Enabled:         model.Enabled,
		HouseIDs:        model.HouseIds,
		Rules:           model.Rules,
		Settings:        model.Settings,
	})
	if err != nil {
		return helpers.RespondLocalizedError(c, controller.localizer, err)
	}
	return c.Status(fiber.StatusOK).JSON(core.Success(dtos.GameConfigToModel(entry.Definition, entry.Config)))
}
