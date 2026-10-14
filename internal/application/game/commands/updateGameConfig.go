package commands

import (
	"context"
	"encoding/json"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	"houseflowApi/internal/infrastructure/cqrs"
)

// UpdateGameConfigCommand replaces a game's stored config. Rules and settings
// omitted from the request fall back to the code defaults. The change applies
// to sessions created afterwards; existing sessions keep their frozen copy.
type UpdateGameConfigCommand struct {
	cqrs.Request[gameApplication.CatalogEntry]
	GameKey         string
	UserID          string
	ExpectedVersion int64
	Enabled         bool
	HouseIDs        []string
	Rules           json.RawMessage
	Settings        json.RawMessage
}

type UpdateGameConfigHandler struct {
	catalog    *gameApplication.Catalog
	repository gameAbstract.GameConfigRepository
	report     func(error)
}

// report receives propagation failures after the change is already stored;
// instances that missed it converge on their next periodic reload.
func NewUpdateGameConfigHandler(catalog *gameApplication.Catalog, repository gameAbstract.GameConfigRepository, report func(error)) *UpdateGameConfigHandler {
	return &UpdateGameConfigHandler{catalog: catalog, repository: repository, report: report}
}

func (handler *UpdateGameConfigHandler) Handle(ctx context.Context, command UpdateGameConfigCommand) (gameApplication.CatalogEntry, error) {
	definition, err := handler.catalog.Find(command.GameKey)
	if err != nil {
		return gameApplication.CatalogEntry{}, gameApplication.MapError(err)
	}
	record := gameAbstract.GameConfigRecord{GameKey: command.GameKey, Enabled: command.Enabled, HouseIDs: command.HouseIDs, Rules: command.Rules, Settings: command.Settings, UpdatedBy: command.UserID}
	if _, err := handler.catalog.Resolve(record); err != nil {
		return gameApplication.CatalogEntry{}, gameApplication.MapError(err)
	}
	stored, err := handler.repository.Save(ctx, record, command.ExpectedVersion)
	if err != nil {
		return gameApplication.CatalogEntry{}, gameApplication.MapError(err)
	}
	config, err := handler.catalog.Resolve(stored)
	if err != nil {
		return gameApplication.CatalogEntry{}, gameApplication.MapError(err)
	}
	if err := handler.catalog.Changed(ctx, command.GameKey); err != nil && handler.report != nil {
		handler.report(err)
	}
	return gameApplication.CatalogEntry{Definition: definition, Config: config}, nil
}

var _ cqrs.CommandHandler[UpdateGameConfigCommand, gameApplication.CatalogEntry] = (*UpdateGameConfigHandler)(nil)
