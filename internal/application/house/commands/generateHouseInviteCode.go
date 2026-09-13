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

const houseInviteCodeLength = 8

type GenerateHouseInviteCodeCommand struct {
	cqrs.Request[*dtos.HouseInviteCodeResponseModel]
	HouseID string
	UserID  string
}

type GenerateHouseInviteCodeHandler struct {
	houseRepository       databaseAbstract.DbRepository[entities.House]
	houseInviteRepository databaseAbstract.DbRepository[entities.HouseInviteCode]
	secret                string
	validityMinutes       int
}

func NewGenerateHouseInviteCodeHandler(
	houseRepository databaseAbstract.DbRepository[entities.House],
	houseInviteRepository databaseAbstract.DbRepository[entities.HouseInviteCode],
	secret string,
	validityMinutes int,
) *GenerateHouseInviteCodeHandler {
	return &GenerateHouseInviteCodeHandler{
		houseRepository:       houseRepository,
		houseInviteRepository: houseInviteRepository,
		secret:                secret,
		validityMinutes:       validityMinutes,
	}
}

func (h *GenerateHouseInviteCodeHandler) Handle(
	ctx context.Context,
	command GenerateHouseInviteCodeCommand,
) (*dtos.HouseInviteCodeResponseModel, error) {
	houseObjectID, err := helpers.ToMongoId(command.HouseID)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_house_id")
	}

	house, err := h.houseRepository.FindByID(ctx, houseObjectID)
	if err != nil {
		if helpers.IsApplicationError(err, "database.error.document_not_found") {
			return nil, helpers.NewLocalizedError("house.error.not_found")
		}
		return nil, err
	}
	if house.OwnerId != command.UserID {
		return nil, helpers.NewLocalizedError("house.error.only_owner_can_generate_invite_code")
	}

	validity := time.Duration(h.validityMinutes) * time.Minute
	for attempt := 0; attempt < 3; attempt++ {
		code, err := helpers.GenerateInviteCode(houseInviteCodeLength)
		if err != nil {
			return nil, helpers.NewLocalizedError("house.error.failed_generate_invite_code")
		}

		now := time.Now().UTC()
		digest := helpers.GenerateInviteCodeDigest(code, h.secret)
		_, err = h.houseInviteRepository.Collection().UpdateOne(ctx,
			bson.M{"_id": houseObjectID},
			bson.M{
				"$set": bson.M{
					"codeDigest":  digest,
					"expiresAt":   now.Add(validity),
					"generatedBy": command.UserID,
					"generatedOn": now,
				},
			},
			options.Update().SetUpsert(true),
		)
		if err == nil {
			return &dtos.HouseInviteCodeResponseModel{
				InviteCode:       code,
				ExpiresInSeconds: int(validity.Seconds()),
			}, nil
		}
		if !mongo.IsDuplicateKeyError(err) {
			return nil, err
		}
	}

	return nil, helpers.NewLocalizedError("house.error.failed_generate_invite_code")
}

var _ cqrs.CommandHandler[GenerateHouseInviteCodeCommand, *dtos.HouseInviteCodeResponseModel] = (*GenerateHouseInviteCodeHandler)(nil)
