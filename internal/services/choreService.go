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

const (
	maxChoreReviewRounds = 3
	systemCompletedBy    = "system"
)

type ChoreService struct {
	dbRepository              *abstract.DbRepository[entities.Chore]
	houseRepository           *abstract.DbRepository[entities.House]
	userRepository            *abstract.DbRepository[entities.User]
	choreStatusHistRepository *abstract.DbRepository[entities.ChoreStatusHistory]
	choreReviewVoteRepository *abstract.DbRepository[entities.ChoreReviewVote]
}

func NewChoreService(
	dbRepository *abstract.DbRepository[entities.Chore],
	houseRepository *abstract.DbRepository[entities.House],
	userRepository *abstract.DbRepository[entities.User],
	client *mongo.Client,
	dbName string,
) *ChoreService {
	return &ChoreService{
		dbRepository:              dbRepository,
		houseRepository:           houseRepository,
		userRepository:            userRepository,
		choreStatusHistRepository: abstract.New[entities.ChoreStatusHistory](client, dbName),
		choreReviewVoteRepository: abstract.New[entities.ChoreReviewVote](client, dbName),
	}
}

func (r *ChoreService) validateHouseMember(houseId string, userId string) (*entities.House, error) {
	houseObjectId, err := helpers.ToMongoId(houseId)
	if err != nil {
		return nil, errors.New("invalid house ID format")
	}
	house, err := r.houseRepository.FindById(houseObjectId)
	if err != nil {
		return nil, errors.New("house not found")
	}
	if !stringContains(house.MemberIds, userId) {
		return nil, errors.New("forbidden: user is not a member of this house")
	}
	return house, nil
}

func (r *ChoreService) validateAssignee(house *entities.House, assigneeId string) error {
	assigneeObjectId, err := helpers.ToMongoId(assigneeId)
	if err != nil {
		return errors.New("invalid assignee ID format")
	}
	if !stringContains(house.MemberIds, assigneeId) {
		return errors.New("assigned user is not a member of this house")
	}
	if _, err := r.userRepository.FindById(assigneeObjectId); err != nil {
		return errors.New("assigned user not found")
	}
	return nil
}

func (r *ChoreService) addStatusHistory(choreId string, status entities.ChoreStatus, updaterId string) error {
	statusHistory := entities.ChoreStatusHistory{
		ChoreId:  choreId,
		Status:   status,
		DateTime: time.Now(),
		Updater:  updaterId,
	}
	_, err := r.choreStatusHistRepository.Insert(statusHistory)
	return err
}

func nextChoreStatus(current entities.ChoreStatus) (entities.ChoreStatus, bool) {
	switch current {
	case entities.Draft:
		return entities.Progress, true
	case entities.Progress:
		return entities.InTest, true
	default:
		return current, false
	}
}

func (r *ChoreService) CreateChore(chore dtos.CreateChoreModel, requesterId string) (*dtos.ChoreResponseModel, error) {
	house, err := r.validateHouseMember(chore.HouseId, requesterId)
	if err != nil {
		return nil, err
	}
	if err := r.validateAssignee(house, chore.AssignedTo); err != nil {
		return nil, err
	}

	entity := chore.ToEntity(house.OwnerId)

	createdChore, err := r.dbRepository.Insert(entity)
	if err != nil {
		return nil, err
	}

	exists, err := r.choreStatusHistRepository.ExistsByFilter(bson.M{"choreId": createdChore.Id.Hex(), "status": entities.Draft})
	if err != nil {
		return nil, err
	}

	if !exists {
		if err := r.addStatusHistory(createdChore.Id.Hex(), entities.Draft, requesterId); err != nil {
			return nil, err
		}
	}

	response := dtos.ChoreToResponseModel(*createdChore, nil)
	return &response, nil
}

func (r *ChoreService) UpdateChore(id string, chore dtos.CreateChoreModel, requesterId string) (*dtos.ChoreResponseModel, error) {

	mongoId, err := helpers.ToMongoId(id)
	if err != nil {
		return nil, err
	}
	currentChore, err := r.dbRepository.FindById(mongoId)
	if err != nil {
		return nil, err
	}
	if currentChore.HouseId != chore.HouseId {
		return nil, errors.New("chore house cannot be changed")
	}
	house, err := r.validateHouseMember(currentChore.HouseId, requesterId)
	if err != nil {
		return nil, err
	}
	if err := r.validateAssignee(house, chore.AssignedTo); err != nil {
		return nil, err
	}

	entity := chore.ToEntity(currentChore.HouseOwnerId)
	entity.CreatedOn = currentChore.CreatedOn
	entity.IsCompleted = currentChore.IsCompleted
	entity.CompletedAt = currentChore.CompletedAt
	entity.CompletedBy = currentChore.CompletedBy
	entity.Status = currentChore.Status
	entity.ReviewRound = currentChore.ReviewRound
	updatedChore, err := r.dbRepository.Update(mongoId, entity)
	if err != nil {
		return nil, err
	}
	updatedChore.Id = currentChore.Id

	response := dtos.ChoreToResponseModel(*updatedChore, nil)
	return &response, nil
}

func (r *ChoreService) advanceChoreStatus(currentChore *entities.Chore, targetStatus entities.ChoreStatus, house *entities.House, userId string) error {
	if currentChore.AssignedTo != userId {
		return errors.New("only assigned user can advance chore status")
	}

	nextStatus, ok := nextChoreStatus(currentChore.Status)
	if !ok || targetStatus != nextStatus {
		return errors.New("invalid chore status transition")
	}

	currentChore.Status = targetStatus
	currentChore.IsCompleted = false
	currentChore.CompletedBy = ""
	currentChore.CompletedAt = time.Time{}
	if targetStatus == entities.InTest {
		if currentChore.ReviewRound >= maxChoreReviewRounds {
			return errors.New("maximum review round limit reached")
		}
		currentChore.ReviewRound++
		if len(house.MemberIds) <= 1 {
			currentChore.Status = entities.Completed
			currentChore.IsCompleted = true
			currentChore.CompletedBy = currentChore.AssignedTo
			currentChore.CompletedAt = time.Now()
		}
	}
	return nil
}

func (r *ChoreService) UpdateChoreStatusBulk(model dtos.BulkUpdateChoreStatusModel, userId string) (bool, error) {

	if len(model.Chores) == 0 {
		return false, nil
	}
	house, err := r.validateHouseMember(model.HouseId, userId)
	if err != nil {
		return false, err
	}

	choreIdMap := make(map[string]bool)
	for _, update := range model.Chores {
		if choreIdMap[update.ChoreId] {
			return false, errors.New("Duplicate choreId: " + update.ChoreId)
		}
		choreIdMap[update.ChoreId] = true
	}

	for _, update := range model.Chores {
		mongoId, err := helpers.ToMongoId(update.ChoreId)
		if err != nil {
			return false, errors.New("invalid choreId: " + update.ChoreId)
		}

		currentChore, err := r.dbRepository.FindById(mongoId)
		if err != nil {
			return false, errors.New("chore not found: " + update.ChoreId)
		}
		if currentChore.HouseId != model.HouseId {
			return false, errors.New("chore " + update.ChoreId + " does not belong to the given house")
		}

		previousStatus := currentChore.Status
		if err := r.advanceChoreStatus(currentChore, update.Status, house, userId); err != nil {
			return false, err
		}

		_, err = r.dbRepository.Update(mongoId, *currentChore)
		if err != nil {
			return false, err
		}

		if err := r.addStatusHistory(update.ChoreId, update.Status, userId); err != nil {
			return false, err
		}
		if previousStatus == entities.Progress && currentChore.Status == entities.Completed {
			if err := r.addStatusHistory(update.ChoreId, entities.Completed, userId); err != nil {
				return false, err
			}
		}
	}

	return true, nil
}

func (r *ChoreService) ReviewChore(model dtos.ReviewChoreModel, reviewerId string) (*dtos.ChoreResponseModel, error) {
	choreObjectId, err := helpers.ToMongoId(model.ChoreId)
	if err != nil {
		return nil, err
	}

	currentChore, err := r.dbRepository.FindById(choreObjectId)
	if err != nil {
		return nil, err
	}
	if currentChore.Status != entities.InTest {
		return nil, errors.New("chore is not in review")
	}

	house, err := r.validateHouseMember(currentChore.HouseId, reviewerId)
	if err != nil {
		return nil, err
	}
	if currentChore.AssignedTo == reviewerId {
		return nil, errors.New("assigned user cannot review own chore")
	}

	vote := entities.ChoreReviewVote{
		ChoreId:     model.ChoreId,
		HouseId:     currentChore.HouseId,
		ReviewRound: currentChore.ReviewRound,
		ReviewerId:  reviewerId,
		IsApproved:  *model.IsApproved,
		CreatedOn:   time.Now(),
	}
	if _, err := r.choreReviewVoteRepository.Insert(vote); err != nil {
		return nil, errors.New("review vote already exists for this chore round")
	}

	if !*model.IsApproved {
		nextStatus := entities.Progress
		historyUpdater := reviewerId
		if currentChore.ReviewRound >= maxChoreReviewRounds {
			nextStatus = entities.Completed
			historyUpdater = systemCompletedBy
			currentChore.IsCompleted = true
			currentChore.CompletedBy = systemCompletedBy
			currentChore.CompletedAt = time.Now()
		} else {
			currentChore.IsCompleted = false
			currentChore.CompletedBy = ""
			currentChore.CompletedAt = time.Time{}
		}
		currentChore.Status = nextStatus
		if _, err := r.dbRepository.Update(choreObjectId, *currentChore); err != nil {
			return nil, err
		}
		if err := r.addStatusHistory(model.ChoreId, nextStatus, historyUpdater); err != nil {
			return nil, err
		}
		votes, err := r.allReviewVotes(model.ChoreId)
		if err != nil {
			return nil, err
		}
		response := dtos.ChoreToResponseModelWithReview(*currentChore, nil, votes)
		return &response, nil
	}

	approvedVotes, err := r.choreReviewVoteRepository.FindManyByFilter(bson.M{
		"choreId":     model.ChoreId,
		"reviewRound": currentChore.ReviewRound,
		"isApproved":  true,
	})
	if err != nil {
		return nil, err
	}

	requiredApprovals := len(house.MemberIds) - 1
	if len(approvedVotes) >= requiredApprovals {
		currentChore.Status = entities.Completed
		currentChore.IsCompleted = true
		currentChore.CompletedBy = currentChore.AssignedTo
		currentChore.CompletedAt = time.Now()
		if _, err := r.dbRepository.Update(choreObjectId, *currentChore); err != nil {
			return nil, err
		}
		if err := r.addStatusHistory(model.ChoreId, entities.Completed, reviewerId); err != nil {
			return nil, err
		}
	}

	votes, err := r.allReviewVotes(model.ChoreId)
	if err != nil {
		return nil, err
	}
	response := dtos.ChoreToResponseModelWithReview(*currentChore, nil, votes)
	return &response, nil
}

func (r *ChoreService) allReviewVotes(choreId string) ([]entities.ChoreReviewVote, error) {
	return r.choreReviewVoteRepository.FindManyByFilter(bson.M{
		"choreId": choreId,
	})
}
