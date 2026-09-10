package queries

import (
	"context"
	"time"

	housePolicies "houseflowApi/internal/application/house/policies"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type GetHouseDetailsQuery struct {
	cqrs.Request[*dtos.HouseDetailsModel]
	HouseID     string
	RequesterID string
}

type GetHouseDetailsHandler struct {
	membershipPolicy          *housePolicies.MembershipPolicy
	houseRepository           databaseAbstract.DbRepository[entities.House]
	userRepository            databaseAbstract.DbRepository[entities.User]
	choreRepository           databaseAbstract.DbRepository[entities.Chore]
	choreStatusHistRepository databaseAbstract.DbRepository[entities.ChoreStatusHistory]
	choreReviewVoteRepository databaseAbstract.DbRepository[entities.ChoreReviewVote]
	announcementRepository    databaseAbstract.DbRepository[entities.Announcement]
}

func NewGetHouseDetailsHandler(
	membershipPolicy *housePolicies.MembershipPolicy,
	houseRepository databaseAbstract.DbRepository[entities.House],
	userRepository databaseAbstract.DbRepository[entities.User],
	choreRepository databaseAbstract.DbRepository[entities.Chore],
	choreStatusHistRepository databaseAbstract.DbRepository[entities.ChoreStatusHistory],
	choreReviewVoteRepository databaseAbstract.DbRepository[entities.ChoreReviewVote],
	announcementRepository databaseAbstract.DbRepository[entities.Announcement],
) *GetHouseDetailsHandler {
	return &GetHouseDetailsHandler{
		membershipPolicy:          membershipPolicy,
		houseRepository:           houseRepository,
		userRepository:            userRepository,
		choreRepository:           choreRepository,
		choreStatusHistRepository: choreStatusHistRepository,
		choreReviewVoteRepository: choreReviewVoteRepository,
		announcementRepository:    announcementRepository,
	}
}

func (h *GetHouseDetailsHandler) Handle(ctx context.Context, query GetHouseDetailsQuery) (*dtos.HouseDetailsModel, error) {
	if _, err := helpers.ToMongoId(query.HouseID); err != nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_house_id")
	}

	var details *dtos.HouseDetailsModel
	err := h.houseRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		house, err := h.membershipPolicy.RequireMember(txCtx, query.HouseID, query.RequesterID)
		if err != nil {
			return err
		}

		choreEntities, err := h.choreRepository.FindManyByFilter(txCtx, bson.M{"houseId": query.HouseID})
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
			histories, err := h.choreStatusHistRepository.FindManyByFilter(txCtx, filter,
				options.Find().SetSort(bson.D{{Key: "dateTime", Value: 1}, {Key: "_id", Value: 1}}))
			if err != nil {
				return err
			}
			for _, history := range histories {
				historiesByChore[history.ChoreId] = append(historiesByChore[history.ChoreId], history)
			}

			votes, err := h.choreReviewVoteRepository.FindManyByFilter(txCtx, filter,
				options.Find().SetSort(bson.D{{Key: "createdOn", Value: 1}, {Key: "_id", Value: 1}}))
			if err != nil {
				return err
			}
			for _, vote := range votes {
				votesByChore[vote.ChoreId] = append(votesByChore[vote.ChoreId], vote)
			}
		}

		announcements, err := h.announcementRepository.FindManyByFilter(txCtx,
			activeAnnouncementFilter(query.HouseID, time.Now()),
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
			users, err := h.userRepository.FindManyByFilter(txCtx,
				bson.M{"_id": bson.M{"$in": userIDs}}, options.Find().SetProjection(bson.M{"password": 0}))
			if err != nil {
				return err
			}
			for _, user := range users {
				usersByID[user.Id.Hex()] = user
			}
		}

		details = &dtos.HouseDetailsModel{
			Id:             house.Id.Hex(),
			OwnerId:        house.OwnerId,
			InviteCode:     house.InviteCode,
			Name:           house.Name,
			Type:           house.Type,
			MaxMemberCount: house.MaxMemberCount,
			ProfileImage:   house.ProfileImage,
			CreatedOn:      dtos.NewUTCDateTime(house.CreatedOn),
			UpdatedOn:      dtos.NewUTCDateTime(house.UpdatedOn),
			Members:        make([]dtos.UserResultModel, 0, len(house.MemberIds)),
			Chores:         make([]dtos.ChoreResponseModel, 0, len(choreEntities)),
			Announcements:  make([]dtos.AnnouncementResponseModel, 0, len(announcements)),
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
				dtos.AnnouncementToResponseModel(announcement, dtos.UserDisplayName(usersByID[announcement.UserId])))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return details, nil
}

func activeAnnouncementFilter(houseID string, now time.Time) bson.M {
	return bson.M{
		"houseId":      houseID,
		"createdOn":    bson.M{"$lt": now},
		"displayUntil": bson.M{"$gt": now},
	}
}

var _ cqrs.QueryHandler[GetHouseDetailsQuery, *dtos.HouseDetailsModel] = (*GetHouseDetailsHandler)(nil)
