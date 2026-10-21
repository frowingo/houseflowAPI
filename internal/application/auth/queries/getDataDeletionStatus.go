package queries

import (
	"context"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
)

// GetDataDeletionStatusQuery looks up a data deletion request by the
// confirmation code returned to the provider.
type GetDataDeletionStatusQuery struct {
	cqrs.Request[dtos.DataDeletionStatusResponseModel]
	ConfirmationCode string
}

type GetDataDeletionStatusHandler struct {
	deletionRepository databaseAbstract.DbRepository[entities.DataDeletionRequest]
}

func NewGetDataDeletionStatusHandler(
	deletionRepository databaseAbstract.DbRepository[entities.DataDeletionRequest],
) *GetDataDeletionStatusHandler {
	return &GetDataDeletionStatusHandler{deletionRepository: deletionRepository}
}

func (h *GetDataDeletionStatusHandler) Handle(ctx context.Context, query GetDataDeletionStatusQuery) (dtos.DataDeletionStatusResponseModel, error) {
	request, err := h.deletionRepository.FindOneByFilter(ctx, bson.M{"_id": query.ConfirmationCode})
	if helpers.IsApplicationError(err, "database.error.document_not_found") {
		return dtos.DataDeletionStatusResponseModel{}, helpers.NewNotFoundError("auth.error.data_deletion_not_found")
	}
	if err != nil {
		return dtos.DataDeletionStatusResponseModel{}, err
	}
	return dtos.DataDeletionStatusResponseModel{
		ConfirmationCode: request.Id,
		Status:           request.Status,
		RequestedOn:      dtos.NewUTCDateTime(request.RequestedOn),
	}, nil
}

var _ cqrs.QueryHandler[GetDataDeletionStatusQuery, dtos.DataDeletionStatusResponseModel] = (*GetDataDeletionStatusHandler)(nil)
