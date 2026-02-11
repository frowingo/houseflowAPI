package services

import (
	"errors"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"time"
)

type HouseService struct {
	houseRepository *abstract.DbRepository[entities.House]
	userRepository  *abstract.DbRepository[entities.User]
}

func NewHouseService(
	houseRepository *abstract.DbRepository[entities.House],
	userRepository *abstract.DbRepository[entities.User],
) *HouseService {
	return &HouseService{
		houseRepository: houseRepository,
		userRepository:  userRepository,
	}
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
