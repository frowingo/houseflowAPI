package queries

import (
	"context"
	"sort"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
)

type GetLanguagesQuery struct {
	cqrs.Request[[]dtos.LocalizationLanguageResponseModel]
}

type GetLanguagesHandler struct {
	languageRepository databaseAbstract.DbRepository[entities.LocalizationLanguageOption]
}

func NewGetLanguagesHandler(
	languageRepository databaseAbstract.DbRepository[entities.LocalizationLanguageOption],
) *GetLanguagesHandler {
	return &GetLanguagesHandler{languageRepository: languageRepository}
}

func (h *GetLanguagesHandler) Handle(ctx context.Context, _ GetLanguagesQuery) ([]dtos.LocalizationLanguageResponseModel, error) {
	languages, err := h.languageRepository.FindAll(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(languages, func(i, j int) bool { return languages[i].Code < languages[j].Code })

	response := make([]dtos.LocalizationLanguageResponseModel, 0, len(languages))
	for _, language := range languages {
		response = append(response, languageResponseModel(language))
	}
	return response, nil
}

func languageResponseModel(language entities.LocalizationLanguageOption) dtos.LocalizationLanguageResponseModel {
	return dtos.LocalizationLanguageResponseModel{
		Prefix: language.Code, Name: language.Name, NativeName: language.NativeName,
		IsDefault: language.IsDefault, IsActive: language.IsActive, Image: language.Image,
	}
}

var _ cqrs.QueryHandler[GetLanguagesQuery, []dtos.LocalizationLanguageResponseModel] = (*GetLanguagesHandler)(nil)
