package services

import (
	"errors"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"time"
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

	response := dtos.ChoreToResponseModel(*createdChore)
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

	response := dtos.ChoreToResponseModel(*updatedChore)
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

	response := dtos.ChoreToResponseModel(*updatedChore)
	return &response, nil
}

func (r *ChoreService) UpdateChoreStatusBulk(statusUpdates dtos.BulkUpdateChoreStatusModel, userId string) (bool, error) {

	if len(statusUpdates) == 0 {
		return false, nil
	}

	choreIdMap := make(map[string]bool)
	for _, update := range statusUpdates {
		if choreIdMap[update.ChoreId] {
			return false, errors.New("Duplicate choreId: " + update.ChoreId)
		}
		choreIdMap[update.ChoreId] = true
	}

	// TODO: requestteki tüm chore'ların aynı house'a aitliği kontrolü eklenebilir

	for _, update := range statusUpdates {
		mongoId, err := helpers.ToMongoId(update.ChoreId)
		if err != nil {
			return false, nil
		}

		currentChore, err := r.dbRepository.FindById(mongoId)
		if err != nil {
			return false, nil
		}

		currentChore.Status = update.Status
		_, err = r.dbRepository.Update(mongoId, *currentChore)
		if err != nil {
			return false, nil
		}

		statusHistory := entities.ChoreStatusHistory{
			ChoreId:  update.ChoreId,
			Status:   update.Status,
			DateTime: time.Now(),
			Updater:  userId,
		}
		_, err = r.choreStatusHistRepository.Insert(statusHistory)
		if err != nil {
			return false, nil
		}
	}

	return true, nil
}
