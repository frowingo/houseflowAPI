package commands

import (
	"context"
	"strings"
	"time"

	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type CreateImageAssetCommand struct {
	cqrs.Request[cqrs.NoResult]
	Category string
	FileName string
	FileURL  string
	IsActive bool
}

type CreateImageAssetHandler struct {
	imageRepository *abstract.DbRepository[entities.ImageAsset]
	cache           *helpers.InMemoryCache[[]dtos.ImageAssetResultModel]
}

func NewCreateImageAssetHandler(
	imageRepository *abstract.DbRepository[entities.ImageAsset],
	cache *helpers.InMemoryCache[[]dtos.ImageAssetResultModel],
) *CreateImageAssetHandler {
	return &CreateImageAssetHandler{imageRepository: imageRepository, cache: cache}
}

func (h *CreateImageAssetHandler) Handle(ctx context.Context, command CreateImageAssetCommand) (cqrs.NoResult, error) {
	now := time.Now()
	asset := entities.ImageAsset{
		Category: command.Category, FileName: command.FileName, FileURL: command.FileURL,
		PublicID: strings.ToLower(command.Category) + "_" + strings.ToLower(command.FileName),
		IsActive: command.IsActive, CreatedOn: now, UpdatedOn: now,
	}

	exists, err := h.imageRepository.ExistsByFilter(ctx, bson.M{"publicId": asset.PublicID})
	if err != nil {
		return cqrs.NoResult{}, err
	}
	if exists {
		return cqrs.NoResult{}, helpers.NewConflictError("image_asset.error.duplicate")
	}
	if _, err := h.imageRepository.Insert(ctx, asset); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return cqrs.NoResult{}, helpers.NewConflictError("image_asset.error.duplicate")
		}
		return cqrs.NoResult{}, err
	}

	h.cache.Delete("images_" + asset.Category)
	return cqrs.NoResult{}, nil
}

var _ cqrs.CommandHandler[CreateImageAssetCommand, cqrs.NoResult] = (*CreateImageAssetHandler)(nil)
