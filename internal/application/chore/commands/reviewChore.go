package commands

import (
	"context"
	"time"

	chorePolicies "houseflowApi/internal/application/chore/policies"
	housePolicies "houseflowApi/internal/application/house/policies"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ReviewChoreCommand struct {
	cqrs.Request[*dtos.ChoreResponseModel]
	ChoreID    string
	ReviewerID string
	IsApproved bool
}

type ReviewChoreHandler struct {
	choreRepository         databaseAbstract.DbRepository[entities.Chore]
	statusHistoryRepository databaseAbstract.DbRepository[entities.ChoreStatusHistory]
	reviewVoteRepository    databaseAbstract.DbRepository[entities.ChoreReviewVote]
	membershipPolicy        *housePolicies.MembershipPolicy
	workflowPolicy          *chorePolicies.WorkflowPolicy
}

func NewReviewChoreHandler(
	choreRepository databaseAbstract.DbRepository[entities.Chore],
	statusHistoryRepository databaseAbstract.DbRepository[entities.ChoreStatusHistory],
	reviewVoteRepository databaseAbstract.DbRepository[entities.ChoreReviewVote],
	membershipPolicy *housePolicies.MembershipPolicy,
	workflowPolicy *chorePolicies.WorkflowPolicy,
) *ReviewChoreHandler {
	return &ReviewChoreHandler{
		choreRepository:         choreRepository,
		statusHistoryRepository: statusHistoryRepository,
		reviewVoteRepository:    reviewVoteRepository,
		membershipPolicy:        membershipPolicy,
		workflowPolicy:          workflowPolicy,
	}
}

func (h *ReviewChoreHandler) Handle(ctx context.Context, command ReviewChoreCommand) (*dtos.ChoreResponseModel, error) {
	choreObjectID, err := helpers.ToMongoId(command.ChoreID)
	if err != nil {
		return nil, err
	}

	var reviewedChore *entities.Chore
	err = h.choreRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		currentChore, err := h.choreRepository.FindByID(txCtx, choreObjectID)
		if err != nil {
			return err
		}
		if currentChore.Status != entities.InTest {
			return helpers.NewLocalizedError("chore.error.not_in_review")
		}
		house, err := h.membershipPolicy.RequireMember(txCtx, currentChore.HouseId, command.ReviewerID)
		if err != nil {
			return err
		}
		if currentChore.AssignedTo == command.ReviewerID {
			return helpers.NewLocalizedError("chore.error.assignee_cannot_review_own")
		}

		var lockedChore entities.Chore
		err = h.choreRepository.Collection().FindOneAndUpdate(txCtx, bson.M{
			"_id": choreObjectID, "status": entities.InTest,
			"reviewRound": currentChore.ReviewRound, "version": currentChore.Version,
		}, bson.M{"$inc": bson.M{"version": 1}},
			options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&lockedChore)
		if err == mongo.ErrNoDocuments {
			return helpers.NewConflictError("chore.error.concurrent_update")
		}
		if err != nil {
			return err
		}

		now := time.Now()
		_, err = h.reviewVoteRepository.Insert(txCtx, entities.ChoreReviewVote{
			ChoreId: command.ChoreID, HouseId: lockedChore.HouseId, ReviewRound: lockedChore.ReviewRound,
			ReviewerId: command.ReviewerID, IsApproved: command.IsApproved, CreatedOn: now,
		})
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return helpers.NewConflictError("chore.error.review_vote_already_exists")
			}
			return err
		}

		var approvedCount int64
		if command.IsApproved {
			approvedCount, err = h.reviewVoteRepository.Collection().CountDocuments(txCtx, bson.M{
				"choreId": command.ChoreID, "reviewRound": lockedChore.ReviewRound, "isApproved": true,
			})
			if err != nil {
				return err
			}
		}

		outcome := h.workflowPolicy.ApplyReview(
			&lockedChore,
			command.IsApproved,
			approvedCount,
			len(house.MemberIds),
			command.ReviewerID,
			now,
		)
		if outcome.StatusChanged {
			if err := h.choreRepository.UpdateFields(txCtx, choreObjectID, bson.M{
				"status": lockedChore.Status, "isCompleted": lockedChore.IsCompleted,
				"completedBy": lockedChore.CompletedBy, "completedAt": lockedChore.CompletedAt,
			}); err != nil {
				return err
			}
			if err := addStatusHistory(
				txCtx,
				h.statusHistoryRepository,
				command.ChoreID,
				outcome.HistoryStatus,
				outcome.HistoryUpdater,
				now,
			); err != nil {
				return err
			}
		}
		reviewedChore = &lockedChore
		return nil
	})
	if err != nil {
		return nil, err
	}

	histories, err := h.statusHistoryRepository.FindManyByColumn(ctx, "choreId", reviewedChore.Id.Hex())
	if err != nil {
		return nil, err
	}
	votes, err := h.reviewVoteRepository.FindManyByColumn(ctx, "choreId", reviewedChore.Id.Hex())
	if err != nil {
		return nil, err
	}
	response := dtos.ChoreToResponseModelWithReview(*reviewedChore, histories, votes)
	return &response, nil
}

var _ cqrs.CommandHandler[ReviewChoreCommand, *dtos.ChoreResponseModel] = (*ReviewChoreHandler)(nil)
