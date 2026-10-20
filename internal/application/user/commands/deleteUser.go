package commands

import (
	"context"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DeleteUserCommand struct {
	cqrs.Request[cqrs.NoResult]
	UserID string
}

type DeleteUserHandler struct {
	userRepository     databaseAbstract.DbRepository[entities.User]
	houseRepository    databaseAbstract.DbRepository[entities.House]
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity]
	keepers            map[entities.AuthProvider]authAbstract.SocialAuthorizationKeeper
}

func NewDeleteUserHandler(
	userRepository databaseAbstract.DbRepository[entities.User],
	houseRepository databaseAbstract.DbRepository[entities.House],
	identityRepository databaseAbstract.DbRepository[entities.UserIdentity],
	keepers []authAbstract.SocialAuthorizationKeeper,
) *DeleteUserHandler {
	handler := &DeleteUserHandler{
		userRepository:     userRepository,
		houseRepository:    houseRepository,
		identityRepository: identityRepository,
		keepers:            make(map[entities.AuthProvider]authAbstract.SocialAuthorizationKeeper, len(keepers)),
	}
	for _, keeper := range keepers {
		handler.keepers[keeper.Provider()] = keeper
	}
	return handler
}

func (h *DeleteUserHandler) Handle(ctx context.Context, command DeleteUserCommand) (cqrs.NoResult, error) {
	userObjectID, err := helpers.ToMongoId(command.UserID)
	if err != nil {
		return cqrs.NoResult{}, err
	}
	identities, err := h.identityRepository.FindManyByFilter(ctx, bson.M{"userId": command.UserID})
	if err != nil {
		return cqrs.NoResult{}, err
	}
	err = h.userRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		houses, err := h.houseRepository.FindManyByFilter(txCtx, bson.M{"$or": bson.A{
			bson.M{"ownerId": command.UserID}, bson.M{"memberIds": command.UserID},
		}})
		if err != nil {
			return err
		}

		// A house nobody else belongs to goes away with the account; otherwise
		// a house the user owns passes to its first remaining member.
		var abandonedHouses []primitive.ObjectID
		newOwners := make(map[primitive.ObjectID]string, len(houses))
		for _, house := range houses {
			newOwnerID := firstRemainingMember(house.MemberIds, command.UserID)
			switch {
			case newOwnerID == "":
				abandonedHouses = append(abandonedHouses, house.Id)
			case house.OwnerId == command.UserID:
				newOwners[house.Id] = newOwnerID
			}
		}

		if err := h.userRepository.Delete(txCtx, userObjectID); err != nil {
			return err
		}
		if _, err := h.identityRepository.Collection().DeleteMany(txCtx, bson.M{"userId": command.UserID}); err != nil {
			return err
		}
		database := h.userRepository.Collection().Database()
		if _, err := database.Collection("UserInfoHistory").DeleteMany(txCtx, bson.M{"userId": command.UserID}); err != nil {
			return err
		}
		if err := deleteHouses(txCtx, database, abandonedHouses); err != nil {
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
	if err != nil {
		return cqrs.NoResult{}, err
	}

	h.revokeProviderAuthorizations(ctx, identities)
	return cqrs.NoResult{}, nil
}

// revokeProviderAuthorizations tells providers that require it (Apple) that
// the account is gone. The account is already deleted, so a failure is only
// reported by the adapter and does not fail the request.
func (h *DeleteUserHandler) revokeProviderAuthorizations(ctx context.Context, identities []entities.UserIdentity) {
	for _, identity := range identities {
		keeper, ok := h.keepers[identity.Provider]
		if !ok || identity.AppleRefreshTokenEnc == "" {
			continue
		}
		_ = keeper.Revoke(ctx, identity.AppleRefreshTokenEnc, identity.ProviderClientId)
	}
}

// houseScopedCollections hold documents that belong to a single house through
// their houseId and are deleted together with it.
var houseScopedCollections = []string{
	"Chore", "ChoreReviewVote", "Announcement", "Notification", "HouseInfoHistory",
	"GameSession", "GameMatchResult",
}

func deleteHouses(ctx mongo.SessionContext, database *mongo.Database, houseIDs []primitive.ObjectID) error {
	if len(houseIDs) == 0 {
		return nil
	}
	hexIDs := make(bson.A, 0, len(houseIDs))
	for _, houseID := range houseIDs {
		hexIDs = append(hexIDs, houseID.Hex())
	}
	byHouse := bson.M{"houseId": bson.M{"$in": hexIDs}}

	// Chore status history is keyed by chore, not by house.
	cursor, err := database.Collection("Chore").Find(ctx, byHouse, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	var chores []struct {
		Id primitive.ObjectID `bson:"_id"`
	}
	if err := cursor.All(ctx, &chores); err != nil {
		return err
	}
	if len(chores) > 0 {
		choreIDs := make(bson.A, 0, len(chores))
		for _, chore := range chores {
			choreIDs = append(choreIDs, chore.Id.Hex())
		}
		if _, err := database.Collection("ChoreStatusHistory").DeleteMany(ctx, bson.M{"choreId": bson.M{"$in": choreIDs}}); err != nil {
			return err
		}
	}

	for _, collection := range houseScopedCollections {
		if _, err := database.Collection(collection).DeleteMany(ctx, byHouse); err != nil {
			return err
		}
	}
	byID := bson.M{"_id": bson.M{"$in": houseIDs}}
	if _, err := database.Collection("HouseInviteCode").DeleteMany(ctx, byID); err != nil {
		return err
	}
	_, err = database.Collection("House").DeleteMany(ctx, byID)
	return err
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
