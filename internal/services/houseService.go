package services

import (
	"context"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func stringContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type HouseService struct {
	houseRepository           *abstract.DbRepository[entities.House]
	userRepository            *abstract.DbRepository[entities.User]
	choreRepository           *abstract.DbRepository[entities.Chore]
	choreStatusHistRepository *abstract.DbRepository[entities.ChoreStatusHistory]
	choreReviewVoteRepository *abstract.DbRepository[entities.ChoreReviewVote]
	announcementRepository    *abstract.DbRepository[entities.Announcement]
}

func NewHouseService(
	houseRepository *abstract.DbRepository[entities.House],
	userRepository *abstract.DbRepository[entities.User],
	choreRepository *abstract.DbRepository[entities.Chore],
	client *mongo.Client,
	dbName string,
) *HouseService {
	return &HouseService{
		houseRepository:           houseRepository,
		userRepository:            userRepository,
		choreRepository:           choreRepository,
		choreStatusHistRepository: abstract.New[entities.ChoreStatusHistory](client, dbName),
		choreReviewVoteRepository: abstract.New[entities.ChoreReviewVote](client, dbName),
		announcementRepository:    abstract.New[entities.Announcement](client, dbName),
	}
}

func userDisplayName(user entities.User) string {
	return strings.TrimSpace(user.Firstname + " " + user.Lastname)
}

func (s *HouseService) validateHouseMember(ctx context.Context, houseId string, userId string) (*entities.House, error) {
	houseObjectId, err := helpers.ToMongoId(houseId)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_house_id")
	}

	house, err := s.houseRepository.FindByID(ctx, houseObjectId)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.not_found")
	}
	if !stringContains(house.MemberIds, userId) {
		return nil, helpers.NewLocalizedError("house.error.user_not_member")
	}

	return house, nil
}

func activeAnnouncementFilter(houseId string, now time.Time) bson.M {
	return bson.M{
		"houseId":      houseId,
		"createdOn":    bson.M{"$lt": now},
		"displayUntil": bson.M{"$gt": now},
	}
}

func (s *HouseService) CreateAnnouncement(ctx context.Context, model dtos.CreateAnnouncementModel, userId string) (*dtos.AnnouncementResponseModel, error) {
	userObjectId, err := helpers.ToMongoId(userId)
	if err != nil {
		return nil, helpers.NewLocalizedError("user.error.invalid_user_id")
	}
	now := time.Now()
	var user *entities.User
	var createdAnnouncement *entities.Announcement
	err = s.announcementRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		if _, err := s.validateHouseMember(txCtx, model.HouseId, userId); err != nil {
			return err
		}
		user, err = s.userRepository.FindByID(txCtx, userObjectId)
		if err != nil {
			return helpers.NewLocalizedError("user.error.not_found")
		}

		guardID := model.HouseId + ":" + userId
		guardCollection := s.announcementRepository.Collection().Database().Collection("AnnouncementRateLimit")
		_, err = guardCollection.UpdateOne(txCtx, bson.M{
			"_id": guardID,
			"$or": bson.A{
				bson.M{"nextAllowedAt": bson.M{"$lte": now}},
				bson.M{"nextAllowedAt": bson.M{"$exists": false}},
			},
		}, bson.M{
			"$set":         bson.M{"nextAllowedAt": now.Add(24 * time.Hour)},
			"$setOnInsert": bson.M{"houseId": model.HouseId, "userId": userId},
		}, options.Update().SetUpsert(true))
		if mongo.IsDuplicateKeyError(err) {
			return helpers.NewRateLimitError("announcement.error.only_one_per_24_hours")
		}
		if err != nil {
			return helpers.NewLocalizedError("announcement.error.failed_check_recent")
		}

		createdAnnouncement, err = s.announcementRepository.Insert(txCtx, model.ToEntity(userId, now))
		if err != nil {
			return helpers.NewLocalizedError("announcement.error.failed_create")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	response := dtos.AnnouncementToResponseModel(*createdAnnouncement, userDisplayName(*user))
	return &response, nil
}

// CreateHouse creates a new house with generated invite code
func (s *HouseService) CreateHouse(ctx context.Context, model dtos.CreateHouseModel) (*entities.House, error) {
	ownerObjectId, err := helpers.ToMongoId(model.OwnerId)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_owner_id")
	}

	for attempt := 0; attempt < 3; attempt++ {
		inviteCode, err := helpers.GenerateInviteCode(8)
		if err != nil {
			return nil, helpers.NewLocalizedError("house.error.failed_generate_invite_code")
		}
		var house *entities.House
		err = s.houseRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
			if _, err := s.userRepository.FindByID(txCtx, ownerObjectId); err != nil {
				return helpers.NewLocalizedError("house.error.owner_not_found")
			}
			house, err = s.houseRepository.Insert(txCtx, model.ToEntity(inviteCode))
			if err != nil {
				return err
			}
			result, err := s.userRepository.Collection().UpdateOne(txCtx, bson.M{"_id": ownerObjectId}, bson.M{
				"$addToSet": bson.M{"houseIds": house.Id.Hex()},
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
			return house, nil
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

// GetHouseDetails returns house details with member user objects
func (s *HouseService) GetHouseDetails(ctx context.Context, houseId string, requesterId string) (*dtos.HouseDetailsModel, error) {
	if _, err := helpers.ToMongoId(houseId); err != nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_house_id")
	}
	var details *dtos.HouseDetailsModel
	err := s.houseRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		house, err := s.validateHouseMember(txCtx, houseId, requesterId)
		if err != nil {
			return err
		}
		choreEntities, err := s.choreRepository.FindManyByFilter(txCtx, bson.M{"houseId": houseId})
		if err != nil {
			return err
		}
		choreIDs := make([]string, 0, len(choreEntities))
		for _, chore := range choreEntities {
			choreIDs = append(choreIDs, chore.Id.Hex())
		}
		historiesByChore := make(map[string][]entities.ChoreStatusHistory)
		votesByChore := make(map[string][]entities.ChoreReviewVote)
		if len(choreIDs) > 0 {
			filter := bson.M{"choreId": bson.M{"$in": choreIDs}}
			histories, err := s.choreStatusHistRepository.FindManyByFilter(txCtx, filter,
				options.Find().SetSort(bson.D{{Key: "dateTime", Value: 1}, {Key: "_id", Value: 1}}))
			if err != nil {
				return err
			}
			for _, history := range histories {
				historiesByChore[history.ChoreId] = append(historiesByChore[history.ChoreId], history)
			}
			votes, err := s.choreReviewVoteRepository.FindManyByFilter(txCtx, filter,
				options.Find().SetSort(bson.D{{Key: "createdOn", Value: 1}, {Key: "_id", Value: 1}}))
			if err != nil {
				return err
			}
			for _, vote := range votes {
				votesByChore[vote.ChoreId] = append(votesByChore[vote.ChoreId], vote)
			}
		}

		announcements, err := s.announcementRepository.FindManyByFilter(txCtx,
			activeAnnouncementFilter(houseId, time.Now()),
			options.Find().SetSort(bson.D{{Key: "createdOn", Value: -1}, {Key: "_id", Value: -1}}))
		if err != nil {
			return err
		}
		userIDs := make([]primitive.ObjectID, 0, len(house.MemberIds)+len(announcements))
		seen := make(map[primitive.ObjectID]bool)
		addUserID := func(raw string) {
			id, err := primitive.ObjectIDFromHex(raw)
			if err == nil && !seen[id] {
				seen[id] = true
				userIDs = append(userIDs, id)
			}
		}
		for _, id := range house.MemberIds {
			addUserID(id)
		}
		for _, announcement := range announcements {
			addUserID(announcement.UserId)
		}
		usersByID := make(map[string]entities.User)
		if len(userIDs) > 0 {
			users, err := s.userRepository.FindManyByFilter(txCtx,
				bson.M{"_id": bson.M{"$in": userIDs}}, options.Find().SetProjection(bson.M{"password": 0}))
			if err != nil {
				return err
			}
			for _, user := range users {
				usersByID[user.Id.Hex()] = user
			}
		}

		details = &dtos.HouseDetailsModel{
			Id: house.Id.Hex(), OwnerId: house.OwnerId, InviteCode: house.InviteCode,
			Name: house.Name, Type: house.Type, MaxMemberCount: house.MaxMemberCount,
			ProfileImage: house.ProfileImage, CreatedOn: dtos.NewUTCDateTime(house.CreatedOn),
			UpdatedOn:     dtos.NewUTCDateTime(house.UpdatedOn),
			Members:       make([]dtos.UserResultModel, 0, len(house.MemberIds)),
			Chores:        make([]dtos.ChoreResponseModel, 0, len(choreEntities)),
			Announcements: make([]dtos.AnnouncementResponseModel, 0, len(announcements)),
		}
		for _, id := range house.MemberIds {
			if user, ok := usersByID[id]; ok {
				details.Members = append(details.Members, dtos.UserToResultModel(user))
			}
		}
		for _, chore := range choreEntities {
			id := chore.Id.Hex()
			details.Chores = append(details.Chores,
				dtos.ChoreToResponseModelWithReview(chore, historiesByChore[id], votesByChore[id]))
		}
		for _, announcement := range announcements {
			details.Announcements = append(details.Announcements,
				dtos.AnnouncementToResponseModel(announcement, userDisplayName(usersByID[announcement.UserId])))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return details, nil
}

// JoinHouseByCode allows a user to join a house using an invite code
func (s *HouseService) JoinHouseByCode(ctx context.Context, model dtos.JoinHouseByCodeModel) (*entities.House, error) {
	// Validate user exists
	userObjectId, err := helpers.ToMongoId(model.UserId)
	if err != nil {
		return nil, helpers.NewLocalizedError("user.error.invalid_user_id")
	}

	var updated entities.House
	err = s.houseRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		if _, err := s.userRepository.FindByID(txCtx, userObjectId); err != nil {
			return helpers.NewLocalizedError("user.error.not_found")
		}
		house, err := s.houseRepository.FindByColumn(txCtx, "inviteCode", model.InviteCode)
		if err != nil || house == nil {
			return helpers.NewLocalizedError("house.error.invalid_invite_code")
		}
		if stringContains(house.MemberIds, model.UserId) {
			return helpers.NewConflictError("house.error.user_already_member")
		}
		if len(house.MemberIds) >= house.MaxMemberCount {
			return helpers.NewConflictError("house.error.full")
		}

		now := time.Now()
		filter := bson.M{
			"_id":       house.Id,
			"memberIds": bson.M{"$ne": model.UserId},
			"$expr": bson.M{"$lt": bson.A{
				bson.M{"$size": bson.M{"$ifNull": bson.A{"$memberIds", bson.A{}}}},
				"$maxMemberCount",
			}},
		}
		update := bson.M{"$addToSet": bson.M{"memberIds": model.UserId}, "$set": bson.M{"updatedOn": now}}
		if err := s.houseRepository.Collection().FindOneAndUpdate(txCtx, filter, update,
			options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated); err != nil {
			if err == mongo.ErrNoDocuments {
				return helpers.NewConflictError("house.error.failed_join")
			}
			return err
		}
		result, err := s.userRepository.Collection().UpdateOne(txCtx, bson.M{"_id": userObjectId}, bson.M{
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
	return &updated, nil
}
