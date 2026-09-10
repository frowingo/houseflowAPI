package commands

import (
	"context"

	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

type DeleteUserCommand struct {
	cqrs.Request[cqrs.NoResult]
	UserID string
}

type DeleteUserHandler struct {
	userRepository *abstract.DbRepository[entities.User]
}

func NewDeleteUserHandler(userRepository *abstract.DbRepository[entities.User]) *DeleteUserHandler {
	return &DeleteUserHandler{userRepository: userRepository}
}

func (h *DeleteUserHandler) Handle(ctx context.Context, command DeleteUserCommand) (cqrs.NoResult, error) {
	userObjectID, err := helpers.ToMongoId(command.UserID)
	if err != nil {
		return cqrs.NoResult{}, err
	}
	return cqrs.NoResult{}, h.userRepository.Delete(ctx, userObjectID)
}

var _ cqrs.CommandHandler[DeleteUserCommand, cqrs.NoResult] = (*DeleteUserHandler)(nil)
