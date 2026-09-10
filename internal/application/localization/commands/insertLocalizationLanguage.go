package commands

import (
	"context"
	"strings"
	"time"

	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/mongo"
)

type InsertLocalizationLanguageCommand struct {
	cqrs.Request[cqrs.NoResult]
	Prefix     string
	Name       string
	NativeName string
	IsDefault  bool
	IsActive   bool
	Image      string
}

type InsertLocalizationLanguageHandler struct {
	languageRepository *abstract.DbRepository[entities.LocalizationLanguageOption]
}

func NewInsertLocalizationLanguageHandler(
	languageRepository *abstract.DbRepository[entities.LocalizationLanguageOption],
) *InsertLocalizationLanguageHandler {
	return &InsertLocalizationLanguageHandler{languageRepository: languageRepository}
}

func (h *InsertLocalizationLanguageHandler) Handle(ctx context.Context, command InsertLocalizationLanguageCommand) (cqrs.NoResult, error) {
	prefix := strings.ToLower(strings.TrimSpace(command.Prefix))
	if prefix == "" {
		return cqrs.NoResult{}, helpers.NewLocalizedError("localization.error.unsupported_language")
	}

	now := time.Now()
	_, err := h.languageRepository.Insert(ctx, entities.LocalizationLanguageOption{
		Code: prefix, Name: command.Name, NativeName: command.NativeName,
		IsDefault: command.IsDefault, IsActive: command.IsActive, Image: command.Image,
		CreatedOn: now, UpdatedOn: now,
	})
	if mongo.IsDuplicateKeyError(err) {
		return cqrs.NoResult{}, helpers.NewConflictError("localization.error.duplicate_key")
	}
	return cqrs.NoResult{}, err
}

var _ cqrs.CommandHandler[InsertLocalizationLanguageCommand, cqrs.NoResult] = (*InsertLocalizationLanguageHandler)(nil)
