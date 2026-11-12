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
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// UpsertChoreCatalogCommand inserts items without an id and updates (or, with
// IsDeleted, soft-deletes) items with one. The whole request is one
// transaction; the result keeps the request order.
type UpsertChoreCatalogCommand struct {
	cqrs.Request[[]dtos.ChoreCatalogModel]
	Items         []dtos.ChoreCatalogModel
	RequesterID   string
	RequesterRole int
}

type UpsertChoreCatalogHandler struct {
	catalogRepository databaseAbstract.DbRepository[entities.ChoreCatalog]
}

func NewUpsertChoreCatalogHandler(catalogRepository databaseAbstract.DbRepository[entities.ChoreCatalog]) *UpsertChoreCatalogHandler {
	return &UpsertChoreCatalogHandler{catalogRepository: catalogRepository}
}

func (h *UpsertChoreCatalogHandler) Handle(ctx context.Context, command UpsertChoreCatalogCommand) ([]dtos.ChoreCatalogModel, error) {
	isSuperAdmin := command.RequesterRole == int(entities.SuperAdmin)
	var result []dtos.ChoreCatalogModel
	err := h.catalogRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		// A retried transaction starts from an empty result.
		result = make([]dtos.ChoreCatalogModel, 0, len(command.Items))
		now := time.Now().UTC().Truncate(time.Millisecond)
		for _, item := range command.Items {
			var (
				saved *entities.ChoreCatalog
				err   error
			)
			if item.Id == "" {
				saved, err = h.insert(txCtx, item, command.RequesterID, isSuperAdmin, now)
			} else {
				saved, err = h.update(txCtx, item, command.RequesterID, isSuperAdmin, now)
			}
			if err != nil {
				return err
			}
			result = append(result, dtos.ChoreCatalogToModel(*saved))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (h *UpsertChoreCatalogHandler) insert(
	ctx context.Context, item dtos.ChoreCatalogModel, requesterID string, isSuperAdmin bool, now time.Time,
) (*entities.ChoreCatalog, error) {
	if item.IsDeleted {
		return nil, helpers.NewLocalizedError("choreCatalog.error.delete_requires_id")
	}
	userID := requesterID
	if item.IsSystem {
		if !isSuperAdmin {
			return nil, helpers.NewForbiddenError("choreCatalog.error.system_forbidden")
		}
		userID = entities.SystemUserId
	}
	return h.catalogRepository.Insert(ctx, entities.ChoreCatalog{
		Title:             item.Title,
		Description:       item.Description,
		RecurringInterval: item.RecurringInterval,
		Level:             item.Level,
		Category:          item.Category,
		CreatedOn:         now,
		UpdatedOn:         now,
		IsDeleted:         false,
		UserId:            userID,
	})
}

func (h *UpsertChoreCatalogHandler) update(
	ctx context.Context, item dtos.ChoreCatalogModel, requesterID string, isSuperAdmin bool, now time.Time,
) (*entities.ChoreCatalog, error) {
	id, err := helpers.ToMongoId(item.Id)
	if err != nil {
		return nil, err
	}
	active := bson.M{"_id": id, "isDeleted": false}
	var current entities.ChoreCatalog
	err = h.catalogRepository.Collection().FindOne(ctx, active).Decode(&current)
	if err == mongo.ErrNoDocuments {
		return nil, helpers.NewNotFoundError("choreCatalog.error.not_found")
	}
	if err != nil {
		return nil, err
	}
	if current.IsSystem() {
		if !isSuperAdmin {
			return nil, helpers.NewForbiddenError("choreCatalog.error.system_forbidden")
		}
	} else if current.UserId != requesterID {
		// Another user's entry is reported as missing so its existence does not leak.
		return nil, helpers.NewNotFoundError("choreCatalog.error.not_found")
	}

	fields := bson.M{"updatedOn": now}
	if item.IsDeleted {
		fields["isDeleted"] = true
	} else {
		fields["title"] = item.Title
		fields["description"] = item.Description
		fields["recurringInterval"] = item.RecurringInterval
		fields["level"] = item.Level
		fields["category"] = item.Category
	}
	var updated entities.ChoreCatalog
	err = h.catalogRepository.Collection().FindOneAndUpdate(
		ctx, active, bson.M{"$set": fields}, options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if err == mongo.ErrNoDocuments {
		return nil, helpers.NewNotFoundError("choreCatalog.error.not_found")
	}
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

var _ cqrs.CommandHandler[UpsertChoreCatalogCommand, []dtos.ChoreCatalogModel] = (*UpsertChoreCatalogHandler)(nil)
