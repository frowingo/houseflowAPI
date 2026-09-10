package queries

import (
	"context"
	"sort"

	localizationAbstract "houseflowApi/internal/application/localization/abstract"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
)

type GetPlaintextsQuery struct {
	cqrs.Request[[]dtos.LocalizationPlaintextResponseModel]
	Language string
}

type GetPlaintextsHandler struct {
	localizationRepository databaseAbstract.DbRepository[entities.Localization]
	cache                  localizationAbstract.Cache
}

func NewGetPlaintextsHandler(
	localizationRepository databaseAbstract.DbRepository[entities.Localization],
	cache localizationAbstract.Cache,
) *GetPlaintextsHandler {
	return &GetPlaintextsHandler{localizationRepository: localizationRepository, cache: cache}
}

func (h *GetPlaintextsHandler) Handle(ctx context.Context, query GetPlaintextsQuery) ([]dtos.LocalizationPlaintextResponseModel, error) {
	language := helpers.NormalizeLanguage(query.Language)
	if !helpers.IsSupportedLanguage(language) {
		return nil, helpers.NewLocalizedError("localization.error.unsupported_language")
	}

	if plaintexts, ok := h.cache.GetPlaintexts(language); ok {
		return plaintextResponseModels(language, plaintexts), nil
	}

	items, err := h.localizationRepository.FindManyByFilter(ctx, bson.M{
		"type": entities.Plaintext, "language": language,
	})
	if err != nil {
		return nil, err
	}
	plaintexts := make(map[string]string, len(items))
	for _, item := range items {
		plaintexts[item.Key] = item.Value
	}
	h.cache.MergePlaintexts(language, plaintexts)
	return plaintextResponseModels(language, plaintexts), nil
}

func plaintextResponseModels(language string, plaintexts map[string]string) []dtos.LocalizationPlaintextResponseModel {
	keys := make([]string, 0, len(plaintexts))
	for key := range plaintexts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	response := make([]dtos.LocalizationPlaintextResponseModel, 0, len(keys))
	for _, key := range keys {
		response = append(response, dtos.LocalizationPlaintextResponseModel{
			Key: key, Language: language, Value: plaintexts[key],
		})
	}
	return response
}

var _ cqrs.QueryHandler[GetPlaintextsQuery, []dtos.LocalizationPlaintextResponseModel] = (*GetPlaintextsHandler)(nil)
