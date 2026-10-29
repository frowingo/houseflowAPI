package queries

import (
	"context"
	"strconv"
	"time"

	storageAbstract "houseflowApi/internal/application/storage/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const choreReviewPhotoURLTTL = 5 * time.Minute

type GetReviewPhotoURLQuery struct {
	cqrs.Request[*dtos.ChoreReviewPhotoURLModel]
	ChoreID     string
	Round       int
	RequesterID string
}

type GetReviewPhotoURLHandler struct {
	choreRepository  reviewPhotoChoreReader
	membershipPolicy reviewPhotoMembershipPolicy
	objectStorage    storageAbstract.ObjectStorage
}

type reviewPhotoChoreReader interface {
	FindByID(context.Context, primitive.ObjectID) (*entities.Chore, error)
}

type reviewPhotoMembershipPolicy interface {
	RequireMember(context.Context, string, string) (*entities.House, error)
}

func NewGetReviewPhotoURLHandler(
	choreRepository reviewPhotoChoreReader,
	membershipPolicy reviewPhotoMembershipPolicy,
	objectStorage storageAbstract.ObjectStorage,
) *GetReviewPhotoURLHandler {
	return &GetReviewPhotoURLHandler{
		choreRepository:  choreRepository,
		membershipPolicy: membershipPolicy,
		objectStorage:    objectStorage,
	}
}

func (h *GetReviewPhotoURLHandler) Handle(ctx context.Context, query GetReviewPhotoURLQuery) (*dtos.ChoreReviewPhotoURLModel, error) {
	if query.Round <= 0 {
		return nil, helpers.NewLocalizedError("chore.error.invalid_review_round")
	}
	choreObjectID, err := helpers.ToMongoId(query.ChoreID)
	if err != nil {
		return nil, helpers.NewLocalizedError("chore.error.invalid_chore_id", query.ChoreID)
	}

	chore, err := h.choreRepository.FindByID(ctx, choreObjectID)
	if err != nil {
		if helpers.IsApplicationError(err, "database.error.document_not_found") {
			return nil, helpers.NewNotFoundError("chore.error.not_found", query.ChoreID)
		}
		return nil, err
	}
	if _, err := h.membershipPolicy.RequireMember(ctx, chore.HouseId, query.RequesterID); err != nil {
		return nil, err
	}

	objectKey := chore.ReviewPhotoKeys[strconv.Itoa(query.Round)]
	if objectKey == "" {
		return nil, helpers.NewNotFoundError("chore.error.review_photo_not_found")
	}
	if h.objectStorage == nil {
		return nil, helpers.NewUnavailableError("chore.error.review_photo_storage_unavailable", nil)
	}

	expiresAt := time.Now().Add(choreReviewPhotoURLTTL)
	url, err := h.objectStorage.PresignGet(ctx, objectKey, choreReviewPhotoURLTTL)
	if err != nil {
		return nil, helpers.NewUnavailableError("chore.error.review_photo_url_failed", err)
	}
	return &dtos.ChoreReviewPhotoURLModel{
		URL:       url,
		ExpiresAt: dtos.NewUTCDateTime(expiresAt),
	}, nil
}

var _ cqrs.QueryHandler[GetReviewPhotoURLQuery, *dtos.ChoreReviewPhotoURLModel] = (*GetReviewPhotoURLHandler)(nil)
