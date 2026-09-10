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

type UpdateChoreCommand struct {
	cqrs.Request[*dtos.ChoreResponseModel]
	ChoreID           string
	Title             string
	Description       string
	AssignedTo        string
	DueDate           time.Time
	HouseID           string
	Level             entities.ChoreLevel
	IsRecurring       bool
	RecurringInterval int
	RequesterID       string
}

type UpdateChoreHandler struct {
	choreRepository         *abstract.DbRepository[entities.Chore]
	statusHistoryRepository *abstract.DbRepository[entities.ChoreStatusHistory]
	reviewVoteRepository    *abstract.DbRepository[entities.ChoreReviewVote]
	membershipPolicy        *housePolicies.MembershipPolicy
	assignmentPolicy        *chorePolicies.AssignmentPolicy
}

func NewUpdateChoreHandler(
	choreRepository *abstract.DbRepository[entities.Chore],
	statusHistoryRepository *abstract.DbRepository[entities.ChoreStatusHistory],
	reviewVoteRepository *abstract.DbRepository[entities.ChoreReviewVote],
	membershipPolicy *housePolicies.MembershipPolicy,
	assignmentPolicy *chorePolicies.AssignmentPolicy,
) *UpdateChoreHandler {
	return &UpdateChoreHandler{
		choreRepository:         choreRepository,
		statusHistoryRepository: statusHistoryRepository,
		reviewVoteRepository:    reviewVoteRepository,
		membershipPolicy:        membershipPolicy,
		assignmentPolicy:        assignmentPolicy,
	}
}

func (h *UpdateChoreHandler) Handle(ctx context.Context, command UpdateChoreCommand) (*dtos.ChoreResponseModel, error) {
	choreObjectID, err := helpers.ToMongoId(command.ChoreID)
	if err != nil {
		return nil, err
	}
	currentChore, err := h.choreRepository.FindByID(ctx, choreObjectID)
	if err != nil {
		return nil, err
	}
	if currentChore.HouseId != command.HouseID {
		return nil, helpers.NewLocalizedError("chore.error.house_cannot_change")
	}

	house, err := h.membershipPolicy.RequireMember(ctx, currentChore.HouseId, command.RequesterID)
	if err != nil {
		return nil, err
	}
	if err := h.assignmentPolicy.RequireValidAssignee(ctx, house, command.AssignedTo); err != nil {
		return nil, err
	}

	update := bson.M{"$set": bson.M{
		"title": command.Title, "description": command.Description, "assignedTo": command.AssignedTo,
		"dueDate": command.DueDate, "level": command.Level, "isRecurring": command.IsRecurring,
		"recurringInterval": command.RecurringInterval,
	}, "$inc": bson.M{"version": 1}}
	var updatedChore entities.Chore
	err = h.choreRepository.Collection().FindOneAndUpdate(ctx, bson.M{
		"_id": choreObjectID, "version": currentChore.Version,
	}, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updatedChore)
	if err == mongo.ErrNoDocuments {
		return nil, helpers.NewConflictError("chore.error.concurrent_update")
	}
	if err != nil {
		return nil, err
	}

	histories, err := h.statusHistoryRepository.FindManyByColumn(ctx, "choreId", updatedChore.Id.Hex())
	if err != nil {
		return nil, err
	}
	votes, err := h.reviewVoteRepository.FindManyByColumn(ctx, "choreId", updatedChore.Id.Hex())
	if err != nil {
		return nil, err
	}
	response := dtos.ChoreToResponseModelWithReview(updatedChore, histories, votes)
	return &response, nil
}

var _ cqrs.CommandHandler[UpdateChoreCommand, *dtos.ChoreResponseModel] = (*UpdateChoreHandler)(nil)
