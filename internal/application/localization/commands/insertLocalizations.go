package commands

import (
	"context"
	"time"

	"houseflowApi/internal/abstract"
	localizationAbstract "houseflowApi/internal/application/localization/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/mongo"
)

type LocalizationValue struct {
	Type     string
	Language string
	Key      string
	Value    string
}

type InsertLocalizationsCommand struct {
	cqrs.Request[cqrs.NoResult]
	Localizations []LocalizationValue
}

type InsertLocalizationsHandler struct {
	localizationRepository *abstract.DbRepository[entities.Localization]
	cache                  localizationAbstract.Cache
}

func NewInsertLocalizationsHandler(
	localizationRepository *abstract.DbRepository[entities.Localization],
	cache localizationAbstract.Cache,
) *InsertLocalizationsHandler {
	return &InsertLocalizationsHandler{
		localizationRepository: localizationRepository,
		cache:                  cache,
	}
}

func (h *InsertLocalizationsHandler) Handle(ctx context.Context, command InsertLocalizationsCommand) (cqrs.NoResult, error) {
	if len(command.Localizations) == 0 {
		return cqrs.NoResult{}, helpers.NewLocalizedError("common.error.request_body_required")
	}

	now := time.Now()
	localizations := make([]entities.Localization, 0, len(command.Localizations))
	for _, value := range command.Localizations {
		if !helpers.IsSupportedLanguage(value.Language) {
			return cqrs.NoResult{}, helpers.NewLocalizedError("localization.error.unsupported_language")
		}
		if !helpers.IsSupportedLocalizationType(value.Type) {
			return cqrs.NoResult{}, helpers.NewLocalizedError("localization.error.unsupported_type")
		}
		localizations = append(localizations, entities.Localization{
			Language:  entities.LocalizationLanguage(helpers.NormalizeLanguage(value.Language)),
			Type:      entities.LocalizationType(helpers.NormalizeLocalizationType(value.Type)),
			Key:       value.Key,
			Value:     value.Value,
			CreatedOn: now,
			UpdatedOn: now,
		})
	}

	err := h.localizationRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		for _, localization := range localizations {
			if _, err := h.localizationRepository.Insert(txCtx, localization); err != nil {
				if mongo.IsDuplicateKeyError(err) {
					return helpers.NewConflictError("localization.error.duplicate_key")
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		return cqrs.NoResult{}, err
	}

	for _, localization := range localizations {
		h.cache.SetLocalization(localization)
	}
	return cqrs.NoResult{}, nil
}

var _ cqrs.CommandHandler[InsertLocalizationsCommand, cqrs.NoResult] = (*InsertLocalizationsHandler)(nil)
