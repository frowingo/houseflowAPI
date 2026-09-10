package queries

import (
	"context"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
)

type GetImageByPublicIDQuery struct {
	cqrs.Request[*dtos.ImageAssetResultModel]
	PublicID string
}

type GetImageByPublicIDHandler struct {
	imageRepository databaseAbstract.DbRepository[entities.ImageAsset]
}

func NewGetImageByPublicIDHandler(imageRepository databaseAbstract.DbRepository[entities.ImageAsset]) *GetImageByPublicIDHandler {
	return &GetImageByPublicIDHandler{imageRepository: imageRepository}
}

func (h *GetImageByPublicIDHandler) Handle(ctx context.Context, query GetImageByPublicIDQuery) (*dtos.ImageAssetResultModel, error) {
	asset, err := h.imageRepository.FindByColumn(ctx, "publicId", query.PublicID)
	if err != nil {
		return nil, err
	}
	if !asset.IsActive {
		return nil, helpers.NewLocalizedError("image_asset.error.not_found")
	}
	response := dtos.ToImageAssetResultModel(*asset)
	return &response, nil
}

var _ cqrs.QueryHandler[GetImageByPublicIDQuery, *dtos.ImageAssetResultModel] = (*GetImageByPublicIDHandler)(nil)
