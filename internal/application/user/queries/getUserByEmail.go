package queries

import (
	"context"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
)

type GetUserByEmailQuery struct {
	cqrs.Request[*dtos.UserResultModel]
	Email string
}

type GetUserByEmailHandler struct {
	userRepository databaseAbstract.DbRepository[entities.User]
}

func NewGetUserByEmailHandler(userRepository databaseAbstract.DbRepository[entities.User]) *GetUserByEmailHandler {
	return &GetUserByEmailHandler{userRepository: userRepository}
}

func (h *GetUserByEmailHandler) Handle(ctx context.Context, query GetUserByEmailQuery) (*dtos.UserResultModel, error) {
	user, err := h.userRepository.FindByColumn(ctx, "email", query.Email)
	if err != nil {
		return nil, err
	}
	response := dtos.UserToResultModel(*user)
	return &response, nil
}

var _ cqrs.QueryHandler[GetUserByEmailQuery, *dtos.UserResultModel] = (*GetUserByEmailHandler)(nil)
