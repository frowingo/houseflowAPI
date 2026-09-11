package commands

import (
	"context"
	"time"

	chorePolicies "houseflowApi/internal/application/chore/policies"
	housePolicies "houseflowApi/internal/application/house/policies"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/mongo"
)

type CreateChoreCommand struct {
	cqrs.Request[*dtos.ChoreResponseModel]
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

type CreateChoreHandler struct {
	choreRepository         databaseAbstract.DbRepository[entities.Chore]
	statusHistoryRepository databaseAbstract.DbRepository[entities.ChoreStatusHistory]
	membershipPolicy        *housePolicies.MembershipPolicy
	assignmentPolicy        *chorePolicies.AssignmentPolicy
}

func NewCreateChoreHandler(
	choreRepository databaseAbstract.DbRepository[entities.Chore],
	statusHistoryRepository databaseAbstract.DbRepository[entities.ChoreStatusHistory],
	membershipPolicy *housePolicies.MembershipPolicy,
	assignmentPolicy *chorePolicies.AssignmentPolicy,
) *CreateChoreHandler {
	return &CreateChoreHandler{
		choreRepository:         choreRepository,
		statusHistoryRepository: statusHistoryRepository,
		membershipPolicy:        membershipPolicy,
		assignmentPolicy:        assignmentPolicy,
	}
}

func (h *CreateChoreHandler) Handle(ctx context.Context, command CreateChoreCommand) (*dtos.ChoreResponseModel, error) {
	var createdChore *entities.Chore
	var createdHistory *entities.ChoreStatusHistory
	err := h.choreRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		house, err := h.membershipPolicy.RequireMember(txCtx, command.HouseID, command.RequesterID)
		if err != nil {
			return err
		}
		if err := h.assignmentPolicy.RequireValidAssignee(txCtx, house, command.AssignedTo); err != nil {
			return err
		}

		now := time.Now()
		createdChore, err = h.choreRepository.Insert(txCtx, entities.Chore{
			Title:             command.Title,
			Description:       command.Description,
			AssignedTo:        command.AssignedTo,
			DueDate:           command.DueDate,
			CreatedOn:         now,
			HouseId:           command.HouseID,
			HouseOwnerId:      house.OwnerId,
			Level:             command.Level,
			Status:            entities.Draft,
			IsRecurring:       command.IsRecurring,
			RecurringInterval: command.RecurringInterval,
		})
		if err != nil {
			return err
		}
		createdHistory, err = h.statusHistoryRepository.Insert(txCtx, entities.ChoreStatusHistory{
			ChoreId:  createdChore.Id.Hex(),
			Status:   entities.Draft,
			DateTime: now,
			Updater:  command.RequesterID,
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	response := dtos.ChoreToResponseModelWithReview(
		*createdChore,
		[]entities.ChoreStatusHistory{*createdHistory},
		[]entities.ChoreReviewVote{},
	)
	return &response, nil
}

var _ cqrs.CommandHandler[CreateChoreCommand, *dtos.ChoreResponseModel] = (*CreateChoreHandler)(nil)
