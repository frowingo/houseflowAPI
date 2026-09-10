package commands

import (
	"context"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
)

type UpdateImageAssetCommand struct {
	cqrs.Request[cqrs.NoResult]
	PublicID string
	FileURL  *string
	IsActive *bool
}

type UpdateImageAssetHandler struct {
	imageRepository databaseAbstract.DbRepository[entities.ImageAsset]
	cache           *helpers.InMemoryCache[[]dtos.ImageAssetResultModel]
}

func NewUpdateImageAssetHandler(
	imageRepository databaseAbstract.DbRepository[entities.ImageAsset],
	cache *helpers.InMemoryCache[[]dtos.ImageAssetResultModel],
) *UpdateImageAssetHandler {
	return &UpdateImageAssetHandler{imageRepository: imageRepository, cache: cache}
}

func (h *UpdateImageAssetHandler) Handle(ctx context.Context, command UpdateImageAssetCommand) (cqrs.NoResult, error) {
	asset, err := h.imageRepository.FindByColumn(ctx, "publicId", command.PublicID)
	if err != nil {
		return cqrs.NoResult{}, helpers.NewLocalizedError("image_asset.error.not_found")
	}

	fields := bson.M{"updatedOn": time.Now()}
	if command.FileURL != nil {
		fields["fileUrl"] = *command.FileURL
	}
	if command.IsActive != nil {
		fields["isActive"] = *command.IsActive
	}
	if err := h.imageRepository.UpdateFields(ctx, asset.Id, fields); err != nil {
		return cqrs.NoResult{}, err
	}

	h.cache.Delete("images_" + asset.Category)
	return cqrs.NoResult{}, nil
}

var _ cqrs.CommandHandler[UpdateImageAssetCommand, cqrs.NoResult] = (*UpdateImageAssetHandler)(nil)
