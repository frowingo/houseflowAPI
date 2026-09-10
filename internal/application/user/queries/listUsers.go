package queries

import (
	"context"

	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
)

type ListUsersQuery struct {
	cqrs.Request[[]dtos.UserResultModel]
}

type ListUsersHandler struct {
	userRepository *abstract.DbRepository[entities.User]
}

func NewListUsersHandler(userRepository *abstract.DbRepository[entities.User]) *ListUsersHandler {
	return &ListUsersHandler{userRepository: userRepository}
}

func (h *ListUsersHandler) Handle(ctx context.Context, _ ListUsersQuery) ([]dtos.UserResultModel, error) {
	users, err := h.userRepository.FindAll(ctx)
	if err != nil {
		return nil, err
	}
	responses := make([]dtos.UserResultModel, 0, len(users))
	for _, user := range users {
		responses = append(responses, dtos.UserToResultModel(user))
	}
	return responses, nil
}

var _ cqrs.QueryHandler[ListUsersQuery, []dtos.UserResultModel] = (*ListUsersHandler)(nil)
