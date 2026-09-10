package queries

import (
	"context"

	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
)

type GetImagesByCategoryQuery struct {
	cqrs.Request[[]dtos.ImageAssetResultModel]
	Category string
}

type GetImagesByCategoryHandler struct {
	imageRepository *abstract.DbRepository[entities.ImageAsset]
	cache           *helpers.InMemoryCache[[]dtos.ImageAssetResultModel]
}

func NewGetImagesByCategoryHandler(
	imageRepository *abstract.DbRepository[entities.ImageAsset],
	cache *helpers.InMemoryCache[[]dtos.ImageAssetResultModel],
) *GetImagesByCategoryHandler {
	return &GetImagesByCategoryHandler{imageRepository: imageRepository, cache: cache}
}

func (h *GetImagesByCategoryHandler) Handle(ctx context.Context, query GetImagesByCategoryQuery) ([]dtos.ImageAssetResultModel, error) {
	cacheKey := "images_" + query.Category
	if cachedImages, exists := h.cache.Get(cacheKey); exists {
		return cachedImages, nil
	}

	assets, err := h.imageRepository.FindManyByFilter(ctx, bson.M{"category": query.Category, "isActive": true})
	if err != nil {
		return nil, err
	}
	responses := make([]dtos.ImageAssetResultModel, 0, len(assets))
	for _, asset := range assets {
		responses = append(responses, dtos.ToImageAssetResultModel(asset))
	}
	if len(responses) > 0 {
		h.cache.Set(cacheKey, responses)
	}
	return responses, nil
}

var _ cqrs.QueryHandler[GetImagesByCategoryQuery, []dtos.ImageAssetResultModel] = (*GetImagesByCategoryHandler)(nil)
