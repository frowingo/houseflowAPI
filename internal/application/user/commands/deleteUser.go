package commands

import (
	"context"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type DeleteUserCommand struct {
	cqrs.Request[cqrs.NoResult]
	UserID string
}

type DeleteUserHandler struct {
	userRepository  databaseAbstract.DbRepository[entities.User]
	houseRepository databaseAbstract.DbRepository[entities.House]
}

func NewDeleteUserHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	houseRepository databaseAbstract.DbRepository[entities.House],
) *DeleteUserHandler {
	return &DeleteUserHandler{userRepository: userRepository, houseRepository: houseRepository}
}

func (h *DeleteUserHandler) Handle(ctx context.Context, command DeleteUserCommand) (cqrs.NoResult, error) {
	userObjectID, err := helpers.ToMongoId(command.UserID)
	if err != nil {
		return cqrs.NoResult{}, err
	}
	err = h.userRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		ownedHouses, err := h.houseRepository.FindManyByFilter(txCtx, bson.M{"ownerId": command.UserID})
		if err != nil {
			return err
		}

		newOwners := make(map[primitive.ObjectID]string, len(ownedHouses))
		for _, house := range ownedHouses {
			newOwnerID := firstRemainingMember(house.MemberIds, command.UserID)
			if newOwnerID == "" {
				return helpers.NewConflictError("user.error.cannot_delete_sole_house_owner")
			}
			newOwners[house.Id] = newOwnerID
		}

		if err := h.userRepository.Delete(txCtx, userObjectID); err != nil {
			return err
		}

		now := time.Now()
		if _, err := h.houseRepository.Collection().UpdateMany(
			txCtx,
			bson.M{"memberIds": command.UserID},
			bson.M{
				"$pull": bson.M{"memberIds": command.UserID},
				"$set":  bson.M{"updatedOn": now},
			},
		); err != nil {
			return err
		}

		for houseID, newOwnerID := range newOwners {
			if err := h.houseRepository.UpdateFields(txCtx, houseID, bson.M{
				"ownerId": newOwnerID, "updatedOn": now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return cqrs.NoResult{}, err
}

func firstRemainingMember(memberIDs []string, excludedUserID string) string {
	for _, memberID := range memberIDs {
		if memberID != "" && memberID != excludedUserID {
			return memberID
		}
	}
	return ""
}

var _ cqrs.CommandHandler[DeleteUserCommand, cqrs.NoResult] = (*DeleteUserHandler)(nil)
