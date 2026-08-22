package services

import (
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
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

func (s *HouseService) validateHouseMember(houseId string, userId string) (*entities.House, error) {
	houseObjectId, err := helpers.ToMongoId(houseId)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_house_id")
	}

	house, err := s.houseRepository.FindById(houseObjectId)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.not_found")
	}
	if !stringContains(house.MemberIds, userId) {
		return nil, helpers.NewLocalizedError("house.error.user_not_member")
	}

	return house, nil
}

func recentAnnouncementFilter(houseId string, userId string, now time.Time) bson.M {
	return bson.M{
		"houseId": houseId,
		"userId":  userId,
		"createdOn": bson.M{
			"$gte": now.Add(-24 * time.Hour),
			"$lte": now,
		},
	}
}

func activeAnnouncementFilter(houseId string, now time.Time) bson.M {
	return bson.M{
		"houseId":      houseId,
		"createdOn":    bson.M{"$lt": now},
		"displayUntil": bson.M{"$gt": now},
	}
}

func (s *HouseService) CreateAnnouncement(model dtos.CreateAnnouncementModel, userId string) (*dtos.AnnouncementResponseModel, error) {
	if _, err := s.validateHouseMember(model.HouseId, userId); err != nil {
		return nil, err
	}

	userObjectId, err := helpers.ToMongoId(userId)
	if err != nil {
		return nil, helpers.NewLocalizedError("user.error.invalid_user_id")
	}
	user, err := s.userRepository.FindById(userObjectId)
	if err != nil {
		return nil, helpers.NewLocalizedError("user.error.not_found")
	}

	now := time.Now()
	hasRecentAnnouncement, err := s.announcementRepository.ExistsByFilter(recentAnnouncementFilter(model.HouseId, userId, now))
	if err != nil {
		return nil, helpers.NewLocalizedError("announcement.error.failed_check_recent")
	}
	if hasRecentAnnouncement {
		return nil, helpers.NewLocalizedError("announcement.error.only_one_per_24_hours")
	}

	createdAnnouncement, err := s.announcementRepository.Insert(model.ToEntity(userId, now))
	if err != nil {
		return nil, helpers.NewLocalizedError("announcement.error.failed_create")
	}

	response := dtos.AnnouncementToResponseModel(*createdAnnouncement, userDisplayName(*user))
	return &response, nil
}

func (s *HouseService) getReviewVotes(chores []entities.Chore) (map[string][]entities.ChoreReviewVote, error) {
	votesByChoreId := make(map[string][]entities.ChoreReviewVote)
	choreIds := make(bson.A, 0, len(chores))

	for _, chore := range chores {
		if chore.ReviewRound == 0 {
			continue
		}
		choreIds = append(choreIds, chore.Id.Hex())
	}

	if len(choreIds) == 0 {
		return votesByChoreId, nil
	}

	votes, err := s.choreReviewVoteRepository.FindManyByFilter(bson.M{"choreId": bson.M{"$in": choreIds}})
	if err != nil {
		return nil, err
	}

	for _, vote := range votes {
		votesByChoreId[vote.ChoreId] = append(votesByChoreId[vote.ChoreId], vote)
	}

	return votesByChoreId, nil
}

// CreateHouse creates a new house with generated invite code
func (s *HouseService) CreateHouse(model dtos.CreateHouseModel) (*entities.House, error) {
	// Validate owner exists
	ownerObjectId, err := helpers.ToMongoId(model.OwnerId)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_owner_id")
	}

	owner, err := s.userRepository.FindById(ownerObjectId)
	if err != nil || owner == nil {
		return nil, helpers.NewLocalizedError("house.error.owner_not_found")
	}

	// Generate unique invite code
	inviteCode, err := helpers.GenerateInviteCode(8)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.failed_generate_invite_code")
	}

	// Check if invite code already exists (very rare but possible)
	existingHouse, _ := s.houseRepository.FindByColumn("inviteCode", inviteCode)
	if existingHouse != nil {
		// Try one more time with a new code
		inviteCode, err = helpers.GenerateInviteCode(8)
		if err != nil {
			return nil, helpers.NewLocalizedError("house.error.failed_generate_invite_code")
		}
	}

	// Create house entity
	entity := model.ToEntity(inviteCode)

	// Insert into database
	house, err := s.houseRepository.Insert(entity)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.failed_create_house", err.Error())
	}

	// Update user's house list
	owner.HouseIds = append(owner.HouseIds, house.Id.Hex())
	owner.UpdatedOn = time.Now()
	_, err = s.userRepository.Update(ownerObjectId, *owner)
	if err != nil {
		// House is created but user update failed, log this
		// In production, you might want to handle this better
		return house, nil
	}

	return house, nil
}

// GetHouseDetails returns house details with member user objects
func (s *HouseService) GetHouseDetails(houseId string, requesterId string) (*dtos.HouseDetailsModel, error) {
	house, err := s.validateHouseMember(houseId, requesterId)
	if err != nil {
		return nil, err
	}

	members := make([]dtos.UserResultModel, 0, len(house.MemberIds))
	membersById := make(map[string]entities.User, len(house.MemberIds))
	for _, memberId := range house.MemberIds {
		userObjectId, err := helpers.ToMongoId(memberId)
		if err != nil {
			continue
		}
		user, err := s.userRepository.FindById(userObjectId)
		if err != nil {
			continue
		}
		membersById[memberId] = *user
		members = append(members, dtos.UserToResultModel(*user))
	}

	choreEntities, _ := s.choreRepository.FindManyByColumn("houseId", houseId)
	reviewVotesByChoreId, err := s.getReviewVotes(choreEntities)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.failed_load_chore_review_votes")
	}

	chores := make([]dtos.ChoreResponseModel, 0, len(choreEntities))
	for _, c := range choreEntities {
		histories, _ := s.choreStatusHistRepository.FindManyByColumn("choreId", c.Id.Hex())
		chores = append(chores, dtos.ChoreToResponseModelWithReview(c, histories, reviewVotesByChoreId[c.Id.Hex()]))
	}

	now := time.Now()
	announcementEntities, err := s.announcementRepository.FindManyByFilter(activeAnnouncementFilter(houseId, now))
	if err != nil {
		return nil, helpers.NewLocalizedError("announcement.error.failed_load")
	}
	sort.Slice(announcementEntities, func(i, j int) bool {
		return announcementEntities[i].CreatedOn.After(announcementEntities[j].CreatedOn)
	})

	announcements := make([]dtos.AnnouncementResponseModel, 0, len(announcementEntities))
	for _, announcement := range announcementEntities {
		announcedBy := ""
		if user, ok := membersById[announcement.UserId]; ok {
			announcedBy = userDisplayName(user)
		} else if userObjectId, conversionErr := helpers.ToMongoId(announcement.UserId); conversionErr == nil {
			if user, lookupErr := s.userRepository.FindById(userObjectId); lookupErr == nil {
				announcedBy = userDisplayName(*user)
			}
		}
		announcements = append(announcements, dtos.AnnouncementToResponseModel(announcement, announcedBy))
	}

	return &dtos.HouseDetailsModel{
		Id:             house.Id.Hex(),
		OwnerId:        house.OwnerId,
		InviteCode:     house.InviteCode,
		Name:           house.Name,
		Type:           house.Type,
		Members:        members,
		MaxMemberCount: house.MaxMemberCount,
		ProfileImage:   house.ProfileImage,
		CreatedOn:      dtos.NewUTCDateTime(house.CreatedOn),
		UpdatedOn:      dtos.NewUTCDateTime(house.UpdatedOn),
		Chores:         chores,
		Announcements:  announcements,
	}, nil
}

// JoinHouseByCode allows a user to join a house using an invite code
func (s *HouseService) JoinHouseByCode(model dtos.JoinHouseByCodeModel) (*entities.House, error) {
	// Validate user exists
	userObjectId, err := helpers.ToMongoId(model.UserId)
	if err != nil {
		return nil, helpers.NewLocalizedError("user.error.invalid_user_id")
	}

	user, err := s.userRepository.FindById(userObjectId)
	if err != nil || user == nil {
		return nil, helpers.NewLocalizedError("user.error.not_found")
	}

	// Find house by invite code
	house, err := s.houseRepository.FindByColumn("inviteCode", model.InviteCode)
	if err != nil || house == nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_invite_code")
	}

	// Check if user is already a member
	for _, memberId := range house.MemberIds {
		if memberId == model.UserId {
			return nil, helpers.NewLocalizedError("house.error.user_already_member")
		}
	}

	// Check if house is full
	if len(house.MemberIds) >= house.MaxMemberCount {
		return nil, helpers.NewLocalizedError("house.error.full")
	}

	// Add user to house members
	house.MemberIds = append(house.MemberIds, model.UserId)
	house.UpdatedOn = time.Now()

	// Update house in database
	updatedHouse, err := s.houseRepository.Update(house.Id, *house)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.failed_join", err.Error())
	}

	// Update user's house list
	user.HouseIds = append(user.HouseIds, house.Id.Hex())
	user.UpdatedOn = time.Now()
	_, err = s.userRepository.Update(userObjectId, *user)
	if err != nil {
		// User joined but their profile update failed
		// In production, you might want to handle this better
		return updatedHouse, nil
	}

	return updatedHouse, nil
}
