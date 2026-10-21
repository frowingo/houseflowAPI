package queries

import (
	"context"
	"errors"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ValidateAuthQuery struct {
	cqrs.Request[*dtos.AuthUserResultModel]
	Token string
}

type ValidateAuthHandler struct {
	userRepository     databaseAbstract.DbRepository[entities.User]
	houseRepository    databaseAbstract.DbRepository[entities.House]
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity]
	jwtService         *helpers.JWTService
}

func NewValidateAuthHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	houseRepository databaseAbstract.DbRepository[entities.House],
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity],
	jwtService *helpers.JWTService,
) *ValidateAuthHandler {
	return &ValidateAuthHandler{
		userRepository:     userRepository,
		houseRepository:    houseRepository,
		identityRepository: identityRepository,
		jwtService:         jwtService,
	}
}

func (h *ValidateAuthHandler) Handle(ctx context.Context, query ValidateAuthQuery) (*dtos.AuthUserResultModel, error) {
	tokenData, err := h.jwtService.ValidateToken(query.Token)
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

	houses, err := h.houseRepository.FindManyByFilter(
		ctx,
		bson.M{"memberIds": user.Id.Hex()},
		options.Find().
			SetProjection(bson.M{"name": 1, "profileImage": 1}).
			SetSort(bson.D{{Key: "_id", Value: 1}}),
	)
	if err != nil {
		return nil, err
	}

	identities, err := h.identityRepository.FindManyByFilter(
		ctx,
		bson.M{"userId": user.Id.Hex()},
		options.Find().
			SetProjection(bson.M{"provider": 1}).
			SetSort(bson.D{{Key: "provider", Value: 1}}),
	)
	if err != nil {
		return nil, err
	}

	response := dtos.UserToAuthResultModel(*user, houses, identities)
	return &response, nil
}

var _ cqrs.QueryHandler[ValidateAuthQuery, *dtos.AuthUserResultModel] = (*ValidateAuthHandler)(nil)
