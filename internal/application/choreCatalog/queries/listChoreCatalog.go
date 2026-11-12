package queries

import (
	"context"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ListChoreCatalogQuery lists the system entries and the requester's own
// entries that are not soft-deleted.
type ListChoreCatalogQuery struct {
	cqrs.Request[[]dtos.ChoreCatalogModel]
	RequesterID string
}

type ListChoreCatalogHandler struct {
	catalogRepository databaseAbstract.DbRepository[entities.ChoreCatalog]
}

func NewListChoreCatalogHandler(catalogRepository databaseAbstract.DbRepository[entities.ChoreCatalog]) *ListChoreCatalogHandler {
	return &ListChoreCatalogHandler{catalogRepository: catalogRepository}
}

func (h *ListChoreCatalogHandler) Handle(ctx context.Context, query ListChoreCatalogQuery) ([]dtos.ChoreCatalogModel, error) {
	catalogs, err := h.catalogRepository.FindManyByFilter(ctx, bson.M{
		"userId":    bson.M{"$in": bson.A{entities.SystemUserId, query.RequesterID}},
		"isDeleted": false,
	}, options.Find().SetSort(bson.D{{Key: "category", Value: 1}, {Key: "title", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	result := make([]dtos.ChoreCatalogModel, 0, len(catalogs))
	for _, catalog := range catalogs {
		result = append(result, dtos.ChoreCatalogToModel(catalog))
	}
	return result, nil
}

var _ cqrs.QueryHandler[ListChoreCatalogQuery, []dtos.ChoreCatalogModel] = (*ListChoreCatalogHandler)(nil)
