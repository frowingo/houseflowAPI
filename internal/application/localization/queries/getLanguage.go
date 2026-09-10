package queries

import (
	"context"
	"sort"
	"strings"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
)

type GetLanguageQuery struct {
	cqrs.Request[[]dtos.LocalizationLanguageResponseModel]
	Prefix string
}

type GetLanguageHandler struct {
	languageRepository databaseAbstract.DbRepository[entities.LocalizationLanguageOption]
}

func NewGetLanguageHandler(
	languageRepository databaseAbstract.DbRepository[entities.LocalizationLanguageOption],
) *GetLanguageHandler {
	return &GetLanguageHandler{languageRepository: languageRepository}
}

func (h *GetLanguageHandler) Handle(ctx context.Context, query GetLanguageQuery) ([]dtos.LocalizationLanguageResponseModel, error) {
	prefix := strings.ToLower(strings.TrimSpace(query.Prefix))
	languages, err := h.languageRepository.FindManyByFilter(ctx, bson.M{
		"code": prefix, "isActive": true,
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(languages, func(i, j int) bool { return languages[i].Code < languages[j].Code })

	response := make([]dtos.LocalizationLanguageResponseModel, 0, len(languages))
	for _, language := range languages {
		response = append(response, dtos.LocalizationLanguageResponseModel{
			Prefix: language.Code, Name: language.Name, NativeName: language.NativeName,
			IsDefault: language.IsDefault, IsActive: language.IsActive, Image: language.Image,
		})
	}
	return response, nil
}

var _ cqrs.QueryHandler[GetLanguageQuery, []dtos.LocalizationLanguageResponseModel] = (*GetLanguageHandler)(nil)
