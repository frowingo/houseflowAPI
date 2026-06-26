package services

import (
	"errors"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
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
	}
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
		return nil, errors.New("invalid owner ID format")
	}

	owner, err := s.userRepository.FindById(ownerObjectId)
	if err != nil || owner == nil {
		return nil, errors.New("owner not found")
	}

	// Generate unique invite code
	inviteCode, err := helpers.GenerateInviteCode(8)
	if err != nil {
		return nil, errors.New("failed to generate invite code")
	}

	// Check if invite code already exists (very rare but possible)
	existingHouse, _ := s.houseRepository.FindByColumn("inviteCode", inviteCode)
	if existingHouse != nil {
		// Try one more time with a new code
		inviteCode, err = helpers.GenerateInviteCode(8)
		if err != nil {
			return nil, errors.New("failed to generate invite code")
		}
	}

	// Create house entity
	entity := model.ToEntity(inviteCode)

	// Insert into database
	house, err := s.houseRepository.Insert(entity)
	if err != nil {
		return nil, errors.New("failed to create house: " + err.Error())
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
	houseObjectId, err := helpers.ToMongoId(houseId)
	if err != nil {
		return nil, errors.New("invalid house ID format")
	}

	house, err := s.houseRepository.FindById(houseObjectId)
	if err != nil {
		return nil, errors.New("house not found")
	}
	if !stringContains(house.MemberIds, requesterId) {
		return nil, errors.New("forbidden: user is not a member of this house")
	}

	members := make([]dtos.UserResultModel, 0, len(house.MemberIds))
	for _, memberId := range house.MemberIds {
		userObjectId, err := helpers.ToMongoId(memberId)
		if err != nil {
			continue
		}
		user, err := s.userRepository.FindById(userObjectId)
		if err != nil {
			continue
		}
		members = append(members, dtos.UserToResultModel(*user))
	}

	choreEntities, _ := s.choreRepository.FindManyByColumn("houseId", houseId)
	reviewVotesByChoreId, err := s.getReviewVotes(choreEntities)
	if err != nil {
		return nil, errors.New("failed to load chore review votes")
	}

	chores := make([]dtos.ChoreResponseModel, 0, len(choreEntities))
	for _, c := range choreEntities {
		histories, _ := s.choreStatusHistRepository.FindManyByColumn("choreId", c.Id.Hex())
		chores = append(chores, dtos.ChoreToResponseModelWithReview(c, histories, reviewVotesByChoreId[c.Id.Hex()]))
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
		CreatedOn:      house.CreatedOn,
		UpdatedOn:      house.UpdatedOn,
		Chores:         chores,
	}, nil
}

// JoinHouseByCode allows a user to join a house using an invite code
func (s *HouseService) JoinHouseByCode(model dtos.JoinHouseByCodeModel) (*entities.House, error) {
	// Validate user exists
	userObjectId, err := helpers.ToMongoId(model.UserId)
	if err != nil {
		return nil, errors.New("invalid user ID format")
	}

	user, err := s.userRepository.FindById(userObjectId)
	if err != nil || user == nil {
		return nil, errors.New("user not found")
	}

	// Find house by invite code
	house, err := s.houseRepository.FindByColumn("inviteCode", model.InviteCode)
	if err != nil || house == nil {
		return nil, errors.New("invalid invite code")
	}

	// Check if user is already a member
	for _, memberId := range house.MemberIds {
		if memberId == model.UserId {
			return nil, errors.New("user is already a member of this house")
		}
	}

	// Check if house is full
	if len(house.MemberIds) >= house.MaxMemberCount {
		return nil, errors.New("house is full")
	}

	// Add user to house members
	house.MemberIds = append(house.MemberIds, model.UserId)
	house.UpdatedOn = time.Now()

	// Update house in database
	updatedHouse, err := s.houseRepository.Update(house.Id, *house)
	if err != nil {
		return nil, errors.New("failed to join house: " + err.Error())
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
