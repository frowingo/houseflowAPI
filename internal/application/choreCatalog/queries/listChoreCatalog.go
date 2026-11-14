package queries

import (
	"context"
	"strings"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ListChoreCatalogQuery lists the system entries and the requester's own
// entries that are not soft-deleted. System entries store localization keys
// and are returned in the requester's language.
type ListChoreCatalogQuery struct {
	cqrs.Request[[]dtos.ChoreCatalogModel]
	RequesterID string
	// Language is the token's language, used when the user record has none.
	Language string
}

type ListChoreCatalogHandler struct {
	catalogRepository databaseAbstract.DbRepository[entities.ChoreCatalog]
	userRepository    catalogUserReader
	localizer         helpers.MessageLocalizer
}

type catalogUserReader interface {
	FindByID(context.Context, primitive.ObjectID) (*entities.User, error)
}

func NewListChoreCatalogHandler(
	catalogRepository databaseAbstract.DbRepository[entities.ChoreCatalog],
	userRepository catalogUserReader,
	localizer helpers.MessageLocalizer,
) *ListChoreCatalogHandler {
	return &ListChoreCatalogHandler{
		catalogRepository: catalogRepository,
		userRepository:    userRepository,
		localizer:         localizer,
	}
}

func (h *ListChoreCatalogHandler) Handle(ctx context.Context, query ListChoreCatalogQuery) ([]dtos.ChoreCatalogModel, error) {
	catalogs, err := h.catalogRepository.FindManyByFilter(ctx, bson.M{
		"userId":    bson.M{"$in": bson.A{entities.SystemUserId, query.RequesterID}},
		"isDeleted": false,
	}, options.Find().SetSort(bson.D{{Key: "category", Value: 1}, {Key: "title", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	language := h.requesterLanguage(ctx, query)
	result := make([]dtos.ChoreCatalogModel, 0, len(catalogs))
	for _, catalog := range catalogs {
		model := dtos.ChoreCatalogToModel(catalog)
		if catalog.IsSystem() {
			model.Title = h.localize(ctx, language, catalog.Title)
			model.Description = h.localize(ctx, language, catalog.Description)
		}
		result = append(result, model)
	}
	return result, nil
}

// requesterLanguage prefers the language stored on the user, which follows
// profile changes, over the one in the token.
func (h *ListChoreCatalogHandler) requesterLanguage(ctx context.Context, query ListChoreCatalogQuery) string {
	if userID, err := helpers.ToMongoId(query.RequesterID); err == nil {
		if user, err := h.userRepository.FindByID(ctx, userID); err == nil && user.Language != "" {
			return helpers.NormalizeLanguage(user.Language)
		}
	}
	return helpers.NormalizeLanguage(query.Language)
}

// localize resolves a system entry's key; text that is not a system key is
// returned unchanged.
func (h *ListChoreCatalogHandler) localize(ctx context.Context, language, value string) string {
	if !strings.HasPrefix(value, entities.SystemChoreCatalogKeyPrefix) {
		return value
	}
	return h.localizer.LocalizeMessage(ctx, language, value)
}

var _ cqrs.QueryHandler[ListChoreCatalogQuery, []dtos.ChoreCatalogModel] = (*ListChoreCatalogHandler)(nil)
