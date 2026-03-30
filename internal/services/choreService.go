package services

import (
	"errors"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

type ChoreService struct {
	dbRepository              *abstract.DbRepository[entities.Chore]
	choreStatusHistRepository *abstract.DbRepository[entities.ChoreStatusHistory]
}

func NewChoreService(dbRepository *abstract.DbRepository[entities.Chore]) *ChoreService {
	return &ChoreService{
		dbRepository:              dbRepository,
		choreStatusHistRepository: abstract.New[entities.ChoreStatusHistory](),
	}
}

func (r *ChoreService) CreateChore(chore dtos.CreateChoreModel, houseOwnerId string) (*dtos.ChoreResponseModel, error) {

	entity := chore.ToEntity(houseOwnerId)

	createdChore, err := r.dbRepository.Insert(entity)
	if err != nil {
		return nil, err
	}

	exists, err := r.choreStatusHistRepository.ExistsByFilter(bson.M{"choreId": createdChore.Id.Hex(), "status": entities.Draft})
	if err != nil {
		return nil, err
	}

	if !exists {
		statusHistory := entities.ChoreStatusHistory{
			ChoreId:  createdChore.Id.Hex(),
			Status:   entities.Draft,
			DateTime: time.Now(),
			Updater:  houseOwnerId,
		}
		_, err = r.choreStatusHistRepository.Insert(statusHistory)
		if err != nil {
			return nil, err
		}
	}

	response := dtos.ChoreToResponseModel(*createdChore, nil)
	return &response, nil
}

func (r *ChoreService) UpdateChoreStatus(id string, status entities.ChoreStatus) (*dtos.ChoreResponseModel, error) {

	mongoId, err := helpers.ToMongoId(id)
	if err != nil {
		return nil, err
	}

	// Get current chore
	currentChore, err := r.dbRepository.FindById(mongoId)
	if err != nil {
		return nil, err
	}

	// Update only the status
	currentChore.Status = status
	updatedChore, err := r.dbRepository.Update(mongoId, *currentChore)
	if err != nil {
		return nil, err
	}

	response := dtos.ChoreToResponseModel(*updatedChore, nil)
	return &response, nil
}

func (r *ChoreService) UpdateChore(id string, chore dtos.CreateChoreModel) (*dtos.ChoreResponseModel, error) {

	mongoId, err := helpers.ToMongoId(id)
	if err != nil {
		return nil, err
	}

	entity := chore.ToEntity("")
	updatedChore, err := r.dbRepository.Update(mongoId, entity)
	if err != nil {
		return nil, err
	}

	response := dtos.ChoreToResponseModel(*updatedChore, nil)
	return &response, nil
}

func (r *ChoreService) UpdateChoreStatusBulk(model dtos.BulkUpdateChoreStatusModel, userId string) (bool, error) {

	if len(model.Chores) == 0 {
		return false, nil
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

		currentChore.Status = update.Status
		_, err = r.dbRepository.Update(mongoId, *currentChore)
		if err != nil {
			return false, err
		}

		exists, err := r.choreStatusHistRepository.ExistsByFilter(bson.M{"choreId": update.ChoreId, "status": update.Status})
		if err != nil {
			return false, err
		}
		if exists {
			return false, errors.New("status history already exists for chore: " + update.ChoreId)
		}

		statusHistory := entities.ChoreStatusHistory{
			ChoreId:  update.ChoreId,
			Status:   update.Status,
			DateTime: time.Now(),
			Updater:  userId,
		}
		_, err = r.choreStatusHistRepository.Insert(statusHistory)
		if err != nil {
			return false, err
		}
	}

	return true, nil
}
