package commands

import (
	"context"
	"time"

	housePolicies "houseflowApi/internal/application/house/policies"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type JoinHouseCommand struct {
	cqrs.Request[*entities.House]
	UserID     string
	InviteCode string
}

type JoinHouseHandler struct {
	houseRepository       databaseAbstract.DbRepository[entities.House]
	userRepository        databaseAbstract.DbRepository[entities.User]
	houseInviteRepository databaseAbstract.DbRepository[entities.HouseInviteCode]
	inviteCodeSecret      string
}

func NewJoinHouseHandler(
	houseRepository databaseAbstract.DbRepository[entities.House],
	userRepository databaseAbstract.DbRepository[entities.User],
	houseInviteRepository databaseAbstract.DbRepository[entities.HouseInviteCode],
	inviteCodeSecret string,
) *JoinHouseHandler {
	return &JoinHouseHandler{
		houseRepository:       houseRepository,
		userRepository:        userRepository,
		houseInviteRepository: houseInviteRepository,
		inviteCodeSecret:      inviteCodeSecret,
	}
}

func (h *JoinHouseHandler) Handle(ctx context.Context, command JoinHouseCommand) (*entities.House, error) {
	userObjectID, err := helpers.ToMongoId(command.UserID)
	if err != nil {
		return nil, helpers.NewLocalizedError("user.error.invalid_user_id")
	}

	var updatedHouse entities.House
	err = h.houseRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		if _, err := h.userRepository.FindByID(txCtx, userObjectID); err != nil {
			return helpers.NewLocalizedError("user.error.not_found")
		}

		digest := helpers.GenerateInviteCodeDigest(command.InviteCode, h.inviteCodeSecret)
		var invite entities.HouseInviteCode
		if err := h.houseInviteRepository.Collection().FindOne(txCtx, bson.M{
			"codeDigest": digest,
			"expiresAt":  bson.M{"$gt": time.Now().UTC()},
		}).Decode(&invite); err != nil {
			if err == mongo.ErrNoDocuments {
				return helpers.NewLocalizedError("house.error.invalid_or_expired_invite_code")
			}
			return err
		}

		house, err := h.houseRepository.FindByID(txCtx, invite.Id)
		if err != nil {
			if helpers.IsApplicationError(err, "database.error.document_not_found") {
				return helpers.NewLocalizedError("house.error.invalid_or_expired_invite_code")
			}
			return err
		}
		if housePolicies.ContainsMember(house.MemberIds, command.UserID) {
			return helpers.NewConflictError("house.error.user_already_member")
		}
		if len(house.MemberIds) >= house.MaxMemberCount {
			return helpers.NewConflictError("house.error.full")
		}

		now := time.Now()
		filter := bson.M{
			"_id":       house.Id,
			"memberIds": bson.M{"$ne": command.UserID},
			"$expr": bson.M{"$lt": bson.A{
				bson.M{"$size": bson.M{"$ifNull": bson.A{"$memberIds", bson.A{}}}},
				"$maxMemberCount",
			}},
		}
		update := bson.M{
			"$addToSet": bson.M{"memberIds": command.UserID},
			"$set":      bson.M{"updatedOn": now},
		}
		if err := h.houseRepository.Collection().FindOneAndUpdate(txCtx, filter, update,
			options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updatedHouse); err != nil {
			if err == mongo.ErrNoDocuments {
				return helpers.NewConflictError("house.error.failed_join")
			}
			return err
		}

		result, err := h.userRepository.Collection().UpdateOne(txCtx, bson.M{"_id": userObjectID}, bson.M{
			"$addToSet": bson.M{"houseIds": house.Id.Hex()},
			"$set":      bson.M{"updatedOn": now},
		})
		if err != nil {
			return err
		}
		if result.MatchedCount == 0 {
			return helpers.NewLocalizedError("user.error.not_found")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &updatedHouse, nil
}

var _ cqrs.CommandHandler[JoinHouseCommand, *entities.House] = (*JoinHouseHandler)(nil)
