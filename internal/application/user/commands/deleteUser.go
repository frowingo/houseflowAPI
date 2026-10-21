package commands

import (
	"context"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	userPolicies "houseflowApi/internal/application/user/policies"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
)

type DeleteUserCommand struct {
	cqrs.Request[cqrs.NoResult]
	UserID string
}

type DeleteUserHandler struct {
	accounts *userPolicies.AccountRemoval
}

func NewDeleteUserHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	houseRepository databaseAbstract.DbRepository[entities.House],
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity],
	keepers []authAbstract.SocialAuthorizationKeeper,
) *DeleteUserHandler {
	return &DeleteUserHandler{
		accounts: userPolicies.NewAccountRemoval(userRepository, houseRepository, identityRepository, keepers),
	}
}

func (h *DeleteUserHandler) Handle(ctx context.Context, command DeleteUserCommand) (cqrs.NoResult, error) {
	_, err := h.accounts.Remove(ctx, command.UserID)
	return cqrs.NoResult{}, err
}

var _ cqrs.CommandHandler[DeleteUserCommand, cqrs.NoResult] = (*DeleteUserHandler)(nil)
