package commands

import (
	"context"
	"time"

	"houseflowApi/internal/abstract"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type CreateAnnouncementCommand struct {
	cqrs.Request[*dtos.AnnouncementResponseModel]
	HouseID     string
	UserID      string
	Title       string
	Description string
}

type CreateAnnouncementHandler struct {
	membershipPolicy       *housePolicies.MembershipPolicy
	userRepository         *abstract.DbRepository[entities.User]
	announcementRepository *abstract.DbRepository[entities.Announcement]
}

func NewCreateAnnouncementHandler(
	membershipPolicy *housePolicies.MembershipPolicy,
	userRepository *abstract.DbRepository[entities.User],
	announcementRepository *abstract.DbRepository[entities.Announcement],
) *CreateAnnouncementHandler {
	return &CreateAnnouncementHandler{
		membershipPolicy:       membershipPolicy,
		userRepository:         userRepository,
		announcementRepository: announcementRepository,
	}
}

func (h *CreateAnnouncementHandler) Handle(ctx context.Context, command CreateAnnouncementCommand) (*dtos.AnnouncementResponseModel, error) {
	userObjectID, err := helpers.ToMongoId(command.UserID)
	if err != nil {
		return nil, helpers.NewLocalizedError("user.error.invalid_user_id")
	}

	now := time.Now()
	var user *entities.User
	var createdAnnouncement *entities.Announcement
	err = h.announcementRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		if _, err := h.membershipPolicy.RequireMember(txCtx, command.HouseID, command.UserID); err != nil {
			return err
		}

		user, err = h.userRepository.FindByID(txCtx, userObjectID)
		if err != nil {
			return helpers.NewLocalizedError("user.error.not_found")
		}

		guardID := command.HouseID + ":" + command.UserID
		guardCollection := h.announcementRepository.Collection().Database().Collection("AnnouncementRateLimit")
		_, err = guardCollection.UpdateOne(txCtx, bson.M{
			"_id": guardID,
			"$or": bson.A{
				bson.M{"nextAllowedAt": bson.M{"$lte": now}},
				bson.M{"nextAllowedAt": bson.M{"$exists": false}},
			},
		}, bson.M{
			"$set":         bson.M{"nextAllowedAt": now.Add(24 * time.Hour)},
			"$setOnInsert": bson.M{"houseId": command.HouseID, "userId": command.UserID},
		}, options.Update().SetUpsert(true))
		if mongo.IsDuplicateKeyError(err) {
			return helpers.NewRateLimitError("announcement.error.only_one_per_24_hours")
		}
		if err != nil {
			return helpers.NewLocalizedError("announcement.error.failed_check_recent")
		}

		createdAnnouncement, err = h.announcementRepository.Insert(txCtx, entities.Announcement{
			Title:        command.Title,
			Description:  command.Description,
			UserId:       command.UserID,
			HouseId:      command.HouseID,
			CreatedOn:    now,
			DisplayUntil: now.Add(24 * time.Hour),
		})
		if err != nil {
			return helpers.NewLocalizedError("announcement.error.failed_create")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	response := dtos.AnnouncementToResponseModel(*createdAnnouncement, dtos.UserDisplayName(*user))
	return &response, nil
}

var _ cqrs.CommandHandler[CreateAnnouncementCommand, *dtos.AnnouncementResponseModel] = (*CreateAnnouncementHandler)(nil)
