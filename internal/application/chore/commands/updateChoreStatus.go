package commands

import (
	"context"
	"time"

	"houseflowApi/internal/abstract"
	chorePolicies "houseflowApi/internal/application/chore/policies"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ChoreStatusUpdate struct {
	ChoreID string
	Status  entities.ChoreStatus
}

type UpdateChoreStatusCommand struct {
	cqrs.Request[[]dtos.ChoreResponseModel]
	HouseID string
	Chores  []ChoreStatusUpdate
	UserID  string
}

type UpdateChoreStatusHandler struct {
	choreRepository         *abstract.DbRepository[entities.Chore]
	statusHistoryRepository *abstract.DbRepository[entities.ChoreStatusHistory]
	reviewVoteRepository    *abstract.DbRepository[entities.ChoreReviewVote]
	membershipPolicy        *housePolicies.MembershipPolicy
	workflowPolicy          *chorePolicies.WorkflowPolicy
}

func NewUpdateChoreStatusHandler(
	choreRepository *abstract.DbRepository[entities.Chore],
	statusHistoryRepository *abstract.DbRepository[entities.ChoreStatusHistory],
	reviewVoteRepository *abstract.DbRepository[entities.ChoreReviewVote],
	membershipPolicy *housePolicies.MembershipPolicy,
	workflowPolicy *chorePolicies.WorkflowPolicy,
) *UpdateChoreStatusHandler {
	return &UpdateChoreStatusHandler{
		choreRepository:         choreRepository,
		statusHistoryRepository: statusHistoryRepository,
		reviewVoteRepository:    reviewVoteRepository,
		membershipPolicy:        membershipPolicy,
		workflowPolicy:          workflowPolicy,
	}
}

func (h *UpdateChoreStatusHandler) Handle(ctx context.Context, command UpdateChoreStatusCommand) ([]dtos.ChoreResponseModel, error) {
	if len(command.Chores) == 0 {
		return []dtos.ChoreResponseModel{}, nil
	}

	choreIDs := make(map[string]struct{}, len(command.Chores))
	for _, update := range command.Chores {
		if _, exists := choreIDs[update.ChoreID]; exists {
			return nil, helpers.NewLocalizedError("chore.error.duplicate_chore_id", update.ChoreID)
		}
		choreIDs[update.ChoreID] = struct{}{}
	}

	updatedChores := make([]entities.Chore, 0, len(command.Chores))
	err := h.choreRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		updatedChores = updatedChores[:0]
		house, err := h.membershipPolicy.RequireMember(txCtx, command.HouseID, command.UserID)
		if err != nil {
			return err
		}

		for _, updateRequest := range command.Chores {
			choreObjectID, err := helpers.ToMongoId(updateRequest.ChoreID)
			if err != nil {
				return helpers.NewLocalizedError("chore.error.invalid_chore_id", updateRequest.ChoreID)
			}
			currentChore, err := h.choreRepository.FindByID(txCtx, choreObjectID)
			if err != nil {
				return helpers.NewLocalizedError("chore.error.not_found", updateRequest.ChoreID)
			}
			if currentChore.HouseId != command.HouseID {
				return helpers.NewLocalizedError("chore.error.not_in_house", updateRequest.ChoreID)
			}

			previousStatus := currentChore.Status
			previousVersion := currentChore.Version
			now := time.Now()
			if err := h.workflowPolicy.Advance(currentChore, updateRequest.Status, len(house.MemberIds), command.UserID, now); err != nil {
				return err
			}

			updateFields := bson.M{
				"status": currentChore.Status, "isCompleted": currentChore.IsCompleted,
				"completedBy": currentChore.CompletedBy, "completedAt": currentChore.CompletedAt,
				"reviewRound": currentChore.ReviewRound,
			}
			var updatedChore entities.Chore
			err = h.choreRepository.Collection().FindOneAndUpdate(txCtx, bson.M{
				"_id": choreObjectID, "version": previousVersion, "status": previousStatus,
			}, bson.M{"$set": updateFields, "$inc": bson.M{"version": 1}},
				options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updatedChore)
			if err == mongo.ErrNoDocuments {
				return helpers.NewConflictError("chore.error.concurrent_update")
			}
			if err != nil {
				return err
			}
			if err := addStatusHistory(txCtx, h.statusHistoryRepository, updateRequest.ChoreID, updateRequest.Status, command.UserID, now); err != nil {
				return err
			}
			if previousStatus == entities.Progress && updatedChore.Status == entities.Completed {
				if err := addStatusHistory(txCtx, h.statusHistoryRepository, updateRequest.ChoreID, entities.Completed, command.UserID, now); err != nil {
					return err
				}
			}
			updatedChores = append(updatedChores, updatedChore)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	responses := make([]dtos.ChoreResponseModel, 0, len(updatedChores))
	for _, updatedChore := range updatedChores {
		histories, err := h.statusHistoryRepository.FindManyByColumn(ctx, "choreId", updatedChore.Id.Hex())
		if err != nil {
			return nil, err
		}
		votes, err := h.reviewVoteRepository.FindManyByColumn(ctx, "choreId", updatedChore.Id.Hex())
		if err != nil {
			return nil, err
		}
		responses = append(responses, dtos.ChoreToResponseModelWithReview(updatedChore, histories, votes))
	}
	return responses, nil
}

var _ cqrs.CommandHandler[UpdateChoreStatusCommand, []dtos.ChoreResponseModel] = (*UpdateChoreStatusHandler)(nil)
