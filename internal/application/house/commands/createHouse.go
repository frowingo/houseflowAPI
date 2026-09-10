package commands

import (
	"context"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type CreateHouseCommand struct {
	cqrs.Request[*entities.House]
	OwnerID        string
	Name           string
	Type           entities.HouseType
	MaxMemberCount int
}

type CreateHouseHandler struct {
	houseRepository databaseAbstract.DbRepository[entities.House]
	userRepository  databaseAbstract.DbRepository[entities.User]
}

func NewCreateHouseHandler(
	houseRepository databaseAbstract.DbRepository[entities.House],
	userRepository databaseAbstract.DbRepository[entities.User],
) *CreateHouseHandler {
	return &CreateHouseHandler{
		houseRepository: houseRepository,
		userRepository:  userRepository,
	}
}

func (h *CreateHouseHandler) Handle(ctx context.Context, command CreateHouseCommand) (*entities.House, error) {
	ownerObjectID, err := helpers.ToMongoId(command.OwnerID)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_owner_id")
	}

	for attempt := 0; attempt < 3; attempt++ {
		inviteCode, err := helpers.GenerateInviteCode(8)
		if err != nil {
			return nil, helpers.NewLocalizedError("house.error.failed_generate_invite_code")
		}

		var createdHouse *entities.House
		err = h.houseRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
			if _, err := h.userRepository.FindByID(txCtx, ownerObjectID); err != nil {
				return helpers.NewLocalizedError("house.error.owner_not_found")
			}

			now := time.Now()
			createdHouse, err = h.houseRepository.Insert(txCtx, entities.House{
				OwnerId:        command.OwnerID,
				InviteCode:     inviteCode,
				Name:           command.Name,
				Type:           command.Type,
				MemberIds:      []string{command.OwnerID},
				MaxMemberCount: command.MaxMemberCount,
				CreatedOn:      now,
				UpdatedOn:      now,
			})
			if err != nil {
				return err
			}

			result, err := h.userRepository.Collection().UpdateOne(txCtx, bson.M{"_id": ownerObjectID}, bson.M{
				"$addToSet": bson.M{"houseIds": createdHouse.Id.Hex()},
				"$set":      bson.M{"updatedOn": time.Now()},
			})
			if err != nil {
				return err
			}
			if result.MatchedCount == 0 {
				return helpers.NewLocalizedError("house.error.owner_not_found")
			}
			return nil
		})
		if err == nil {
			return createdHouse, nil
		}
		if helpers.IsApplicationError(err, "house.error.owner_not_found") {
			return nil, err
		}
		if !mongo.IsDuplicateKeyError(err) {
			return nil, helpers.NewLocalizedError("house.error.failed_create_house", err.Error())
		}
	}

	return nil, helpers.NewLocalizedError("house.error.failed_generate_invite_code")
}

var _ cqrs.CommandHandler[CreateHouseCommand, *entities.House] = (*CreateHouseHandler)(nil)
