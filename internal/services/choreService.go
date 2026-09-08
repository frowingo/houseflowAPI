package services

import (
	"context"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
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

func (r *ChoreService) validateHouseMember(ctx context.Context, houseId string, userId string) (*entities.House, error) {
	houseObjectId, err := helpers.ToMongoId(houseId)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.invalid_house_id")
	}
	house, err := r.houseRepository.FindByID(ctx, houseObjectId)
	if err != nil {
		return nil, helpers.NewLocalizedError("house.error.not_found")
	}
	if !stringContains(house.MemberIds, userId) {
		return nil, helpers.NewLocalizedError("house.error.user_not_member")
	}
	return house, nil
}

func (r *ChoreService) validateAssignee(ctx context.Context, house *entities.House, assigneeId string) error {
	assigneeObjectId, err := helpers.ToMongoId(assigneeId)
	if err != nil {
		return helpers.NewLocalizedError("chore.error.invalid_assignee_id")
	}
	if !stringContains(house.MemberIds, assigneeId) {
		return helpers.NewLocalizedError("chore.error.assignee_not_member")
	}
	if _, err := r.userRepository.FindByID(ctx, assigneeObjectId); err != nil {
		return helpers.NewLocalizedError("chore.error.assignee_not_found")
	}
	return nil
}

func (r *ChoreService) addStatusHistory(ctx context.Context, choreId string, status entities.ChoreStatus, updaterId string) error {
	statusHistory := entities.ChoreStatusHistory{
		ChoreId:  choreId,
		Status:   status,
		DateTime: time.Now(),
		Updater:  updaterId,
	}
	_, err := r.choreStatusHistRepository.Insert(ctx, statusHistory)
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

func (r *ChoreService) choreResponse(ctx context.Context, chore entities.Chore) (dtos.ChoreResponseModel, error) {
	histories, err := r.choreStatusHistRepository.FindManyByColumn(ctx, "choreId", chore.Id.Hex())
	if err != nil {
		return dtos.ChoreResponseModel{}, err
	}
	votes, err := r.allReviewVotes(ctx, chore.Id.Hex())
	if err != nil {
		return dtos.ChoreResponseModel{}, err
	}
	return dtos.ChoreToResponseModelWithReview(chore, histories, votes), nil
}

func (r *ChoreService) CreateChore(ctx context.Context, chore dtos.CreateChoreModel, requesterId string) (*dtos.ChoreResponseModel, error) {
	var createdChore *entities.Chore
	err := r.dbRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		house, err := r.validateHouseMember(txCtx, chore.HouseId, requesterId)
		if err != nil {
			return err
		}
		if err := r.validateAssignee(txCtx, house, chore.AssignedTo); err != nil {
			return err
		}
		createdChore, err = r.dbRepository.Insert(txCtx, chore.ToEntity(house.OwnerId))
		if err != nil {
			return err
		}
		return r.addStatusHistory(txCtx, createdChore.Id.Hex(), entities.Draft, requesterId)
	})
	if err != nil {
		return nil, err
	}

	response, err := r.choreResponse(ctx, *createdChore)
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func (r *ChoreService) UpdateChore(ctx context.Context, id string, chore dtos.CreateChoreModel, requesterId string) (*dtos.ChoreResponseModel, error) {

	mongoId, err := helpers.ToMongoId(id)
	if err != nil {
		return nil, err
	}
	currentChore, err := r.dbRepository.FindByID(ctx, mongoId)
	if err != nil {
		return nil, err
	}
	if currentChore.HouseId != chore.HouseId {
		return nil, helpers.NewLocalizedError("chore.error.house_cannot_change")
	}
	house, err := r.validateHouseMember(ctx, currentChore.HouseId, requesterId)
	if err != nil {
		return nil, err
	}
	if err := r.validateAssignee(ctx, house, chore.AssignedTo); err != nil {
		return nil, err
	}

	update := bson.M{"$set": bson.M{
		"title": chore.Title, "description": chore.Description, "assignedTo": chore.AssignedTo,
		"dueDate": chore.DueDate.Time, "level": chore.Level, "isRecurring": chore.IsRecurring,
		"recurringInterval": chore.RecurringInterval,
	}, "$inc": bson.M{"version": 1}}
	var updatedChore entities.Chore
	err = r.dbRepository.Collection().FindOneAndUpdate(ctx, bson.M{
		"_id": mongoId, "version": currentChore.Version,
	}, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updatedChore)
	if err == mongo.ErrNoDocuments {
		return nil, helpers.NewConflictError("chore.error.concurrent_update")
	}
	if err != nil {
		return nil, err
	}

	response, err := r.choreResponse(ctx, updatedChore)
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func (r *ChoreService) advanceChoreStatus(currentChore *entities.Chore, targetStatus entities.ChoreStatus, house *entities.House, userId string) error {
	if currentChore.AssignedTo != userId {
		return helpers.NewLocalizedError("chore.error.only_assignee_can_advance")
	}

	nextStatus, ok := nextChoreStatus(currentChore.Status)
	if !ok || targetStatus != nextStatus {
		return helpers.NewLocalizedError("chore.error.invalid_status_transition")
	}

	currentChore.Status = targetStatus
	currentChore.IsCompleted = false
	currentChore.CompletedBy = ""
	currentChore.CompletedAt = time.Time{}
	if targetStatus == entities.InTest {
		if currentChore.ReviewRound >= maxChoreReviewRounds {
			return helpers.NewLocalizedError("chore.error.max_review_round_reached")
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

func (r *ChoreService) UpdateChoreStatusBulk(ctx context.Context, model dtos.BulkUpdateChoreStatusModel, userId string) ([]dtos.ChoreResponseModel, error) {

	if len(model.Chores) == 0 {
		return []dtos.ChoreResponseModel{}, nil
	}
	choreIdMap := make(map[string]bool)
	for _, update := range model.Chores {
		if choreIdMap[update.ChoreId] {
			return nil, helpers.NewLocalizedError("chore.error.duplicate_chore_id", update.ChoreId)
		}
		choreIdMap[update.ChoreId] = true
	}

	updatedEntities := make([]entities.Chore, 0, len(model.Chores))
	err := r.dbRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		updatedEntities = updatedEntities[:0]
		house, err := r.validateHouseMember(txCtx, model.HouseId, userId)
		if err != nil {
			return err
		}
		for _, updateRequest := range model.Chores {
			mongoID, err := helpers.ToMongoId(updateRequest.ChoreId)
			if err != nil {
				return helpers.NewLocalizedError("chore.error.invalid_chore_id", updateRequest.ChoreId)
			}
			currentChore, err := r.dbRepository.FindByID(txCtx, mongoID)
			if err != nil {
				return helpers.NewLocalizedError("chore.error.not_found", updateRequest.ChoreId)
			}
			if currentChore.HouseId != model.HouseId {
				return helpers.NewLocalizedError("chore.error.not_in_house", updateRequest.ChoreId)
			}

			previousStatus := currentChore.Status
			previousVersion := currentChore.Version
			if err := r.advanceChoreStatus(currentChore, updateRequest.Status, house, userId); err != nil {
				return err
			}
			updateFields := bson.M{
				"status": currentChore.Status, "isCompleted": currentChore.IsCompleted,
				"completedBy": currentChore.CompletedBy, "completedAt": currentChore.CompletedAt,
				"reviewRound": currentChore.ReviewRound,
			}
			var updated entities.Chore
			err = r.dbRepository.Collection().FindOneAndUpdate(txCtx, bson.M{
				"_id": mongoID, "version": previousVersion, "status": previousStatus,
			}, bson.M{"$set": updateFields, "$inc": bson.M{"version": 1}},
				options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
			if err == mongo.ErrNoDocuments {
				return helpers.NewConflictError("chore.error.concurrent_update")
			}
			if err != nil {
				return err
			}
			if err := r.addStatusHistory(txCtx, updateRequest.ChoreId, updateRequest.Status, userId); err != nil {
				return err
			}
			if previousStatus == entities.Progress && updated.Status == entities.Completed {
				if err := r.addStatusHistory(txCtx, updateRequest.ChoreId, entities.Completed, userId); err != nil {
					return err
				}
			}
			updatedEntities = append(updatedEntities, updated)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	responses := make([]dtos.ChoreResponseModel, 0, len(updatedEntities))
	for _, updated := range updatedEntities {
		response, err := r.choreResponse(ctx, updated)
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}
	return responses, nil
}

func (r *ChoreService) ReviewChore(ctx context.Context, model dtos.ReviewChoreModel, reviewerId string) (*dtos.ChoreResponseModel, error) {
	choreObjectId, err := helpers.ToMongoId(model.ChoreId)
	if err != nil {
		return nil, err
	}

	var currentChore *entities.Chore
	err = r.dbRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		current, err := r.dbRepository.FindByID(txCtx, choreObjectId)
		if err != nil {
			return err
		}
		if current.Status != entities.InTest {
			return helpers.NewLocalizedError("chore.error.not_in_review")
		}
		house, err := r.validateHouseMember(txCtx, current.HouseId, reviewerId)
		if err != nil {
			return err
		}
		if current.AssignedTo == reviewerId {
			return helpers.NewLocalizedError("chore.error.assignee_cannot_review_own")
		}

		// Every vote writes the chore document. Concurrent votes therefore
		// conflict and the transaction callback retries against the latest votes.
		var locked entities.Chore
		err = r.dbRepository.Collection().FindOneAndUpdate(txCtx, bson.M{
			"_id": choreObjectId, "status": entities.InTest,
			"reviewRound": current.ReviewRound, "version": current.Version,
		}, bson.M{"$inc": bson.M{"version": 1}},
			options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&locked)
		if err == mongo.ErrNoDocuments {
			return helpers.NewConflictError("chore.error.concurrent_update")
		}
		if err != nil {
			return err
		}

		vote := entities.ChoreReviewVote{
			ChoreId: model.ChoreId, HouseId: locked.HouseId, ReviewRound: locked.ReviewRound,
			ReviewerId: reviewerId, IsApproved: *model.IsApproved, CreatedOn: time.Now(),
		}
		if _, err := r.choreReviewVoteRepository.Insert(txCtx, vote); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return helpers.NewConflictError("chore.error.review_vote_already_exists")
			}
			return err
		}

		historyStatus := entities.ChoreStatus(-1)
		historyUpdater := reviewerId
		if !*model.IsApproved {
			locked.Status = entities.Progress
			locked.IsCompleted = false
			locked.CompletedBy = ""
			locked.CompletedAt = time.Time{}
			if locked.ReviewRound >= maxChoreReviewRounds {
				locked.Status = entities.Completed
				locked.IsCompleted = true
				locked.CompletedBy = systemCompletedBy
				locked.CompletedAt = time.Now()
				historyUpdater = systemCompletedBy
			}
			historyStatus = locked.Status
		} else {
			approvedCount, err := r.choreReviewVoteRepository.Collection().CountDocuments(txCtx, bson.M{
				"choreId": model.ChoreId, "reviewRound": locked.ReviewRound, "isApproved": true,
			})
			if err != nil {
				return err
			}
			if approvedCount >= int64(len(house.MemberIds)-1) {
				locked.Status = entities.Completed
				locked.IsCompleted = true
				locked.CompletedBy = locked.AssignedTo
				locked.CompletedAt = time.Now()
				historyStatus = entities.Completed
			}
		}

		if historyStatus >= 0 {
			if err := r.dbRepository.UpdateFields(txCtx, choreObjectId, bson.M{
				"status": locked.Status, "isCompleted": locked.IsCompleted,
				"completedBy": locked.CompletedBy, "completedAt": locked.CompletedAt,
			}); err != nil {
				return err
			}
			if err := r.addStatusHistory(txCtx, model.ChoreId, historyStatus, historyUpdater); err != nil {
				return err
			}
		}
		currentChore = &locked
		return nil
	})
	if err != nil {
		return nil, err
	}
	response, err := r.choreResponse(ctx, *currentChore)
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func (r *ChoreService) allReviewVotes(ctx context.Context, choreId string) ([]entities.ChoreReviewVote, error) {
	return r.choreReviewVoteRepository.FindManyByFilter(ctx, bson.M{
		"choreId": choreId,
	})
}
