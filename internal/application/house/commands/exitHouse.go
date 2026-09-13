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
)

type ExitHouseCommand struct {
	cqrs.Request[cqrs.NoResult]
	HouseID      string
	TargetUserID string
	RequesterID  string
}

type ExitHouseHandler struct {
	houseRepository       databaseAbstract.DbRepository[entities.House]
	userRepository        databaseAbstract.DbRepository[entities.User]
	houseInviteRepository databaseAbstract.DbRepository[entities.HouseInviteCode]
}

func NewExitHouseHandler(
	houseRepository databaseAbstract.DbRepository[entities.House],
	userRepository databaseAbstract.DbRepository[entities.User],
	houseInviteRepository databaseAbstract.DbRepository[entities.HouseInviteCode],
) *ExitHouseHandler {
	return &ExitHouseHandler{
		houseRepository:       houseRepository,
		userRepository:        userRepository,
		houseInviteRepository: houseInviteRepository,
	}
}

func (h *ExitHouseHandler) Handle(ctx context.Context, command ExitHouseCommand) (cqrs.NoResult, error) {
	houseObjectID, err := helpers.ToMongoId(command.HouseID)
	if err != nil {
		return cqrs.NoResult{}, helpers.NewLocalizedError("house.error.invalid_house_id")
	}
	targetUserObjectID, err := helpers.ToMongoId(command.TargetUserID)
	if err != nil {
		return cqrs.NoResult{}, helpers.NewLocalizedError("user.error.invalid_user_id")
	}
	canonicalHouseID := houseObjectID.Hex()
	canonicalTargetUserID := targetUserObjectID.Hex()

	err = h.houseRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		house, err := h.houseRepository.FindByID(txCtx, houseObjectID)
		if err != nil {
			if helpers.IsApplicationError(err, "database.error.document_not_found") {
				return helpers.NewNotFoundError("house.error.not_found")
			}
			return err
		}
		if !housePolicies.ContainsMember(house.MemberIds, command.RequesterID) {
			return helpers.NewForbiddenError("house.error.user_not_member")
		}
		if !housePolicies.ContainsMember(house.MemberIds, canonicalTargetUserID) {
			return helpers.NewNotFoundError("house.error.target_not_member")
		}

		requesterIsOwner := house.OwnerId == command.RequesterID
		isSelfExit := command.RequesterID == canonicalTargetUserID
		if !isSelfExit && !requesterIsOwner {
			return helpers.NewForbiddenError("house.error.cannot_remove_member")
		}

		if _, err := h.userRepository.FindByID(txCtx, targetUserObjectID); err != nil {
			if helpers.IsApplicationError(err, "database.error.document_not_found") {
				return helpers.NewNotFoundError("user.error.not_found")
			}
			return err
		}

		now := time.Now().UTC()
		setFields := bson.M{"updatedOn": now}
		if requesterIsOwner && isSelfExit {
			newOwnerID := firstHouseMemberExcept(house.MemberIds, canonicalTargetUserID)
			if newOwnerID == "" {
				return helpers.NewConflictError("house.error.sole_owner_cannot_exit")
			}
			setFields["ownerId"] = newOwnerID
		}

		houseResult, err := h.houseRepository.Collection().UpdateOne(txCtx, bson.M{
			"_id":       houseObjectID,
			"memberIds": canonicalTargetUserID,
		}, bson.M{
			"$pull": bson.M{"memberIds": canonicalTargetUserID},
			"$set":  setFields,
		})
		if err != nil {
			return err
		}
		if houseResult.MatchedCount == 0 {
			return helpers.NewConflictError("house.error.exit_conflict")
		}

		userResult, err := h.userRepository.Collection().UpdateOne(txCtx, bson.M{
			"_id":      targetUserObjectID,
			"houseIds": canonicalHouseID,
		}, bson.M{
			"$pull": bson.M{"houseIds": canonicalHouseID},
			"$set":  bson.M{"updatedOn": now},
		})
		if err != nil {
			return err
		}
		if userResult.MatchedCount == 0 {
			return helpers.NewConflictError("house.error.exit_conflict")
		}

		if requesterIsOwner {
			if _, err := h.houseInviteRepository.Collection().DeleteOne(txCtx, bson.M{"_id": houseObjectID}); err != nil {
				return err
			}
		}
		return nil
	})
	return cqrs.NoResult{}, err
}

func firstHouseMemberExcept(memberIDs []string, excludedUserID string) string {
	for _, memberID := range memberIDs {
		if memberID != "" && memberID != excludedUserID {
			return memberID
		}
	}
	return ""
}

var _ cqrs.CommandHandler[ExitHouseCommand, cqrs.NoResult] = (*ExitHouseHandler)(nil)
