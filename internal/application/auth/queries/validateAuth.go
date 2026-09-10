package queries

import (
	"context"
	"errors"
	"time"

	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
)

type ValidateAuthQuery struct {
	cqrs.Request[*dtos.UserResultModel]
	Token string
}

type ValidateAuthHandler struct {
	userRepository *abstract.DbRepository[entities.User]
}

func NewValidateAuthHandler(userRepository *abstract.DbRepository[entities.User]) *ValidateAuthHandler {
	return &ValidateAuthHandler{userRepository: userRepository}
}

func (h *ValidateAuthHandler) Handle(ctx context.Context, query ValidateAuthQuery) (*dtos.UserResultModel, error) {
	tokenData, err := helpers.ValidateToken(query.Token)
	if err != nil {
		return nil, err
	}
	if !time.Now().Before(tokenData.ExpiresAt.Time) {
		return nil, errors.New("token expired")
	}

	userID, err := helpers.ToMongoId(tokenData.Subject)
	if err != nil {
		return nil, err
	}
	user, err := h.userRepository.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	response := dtos.UserToResultModel(*user)
	return &response, nil
}

var _ cqrs.QueryHandler[ValidateAuthQuery, *dtos.UserResultModel] = (*ValidateAuthHandler)(nil)
