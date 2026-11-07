package commands

import (
	"context"
	"time"

	chorePolicies "houseflowApi/internal/application/chore/policies"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func failChoreIfDue(
	ctx context.Context,
	choreRepository databaseAbstract.DbRepository[entities.Chore],
	statusHistoryRepository databaseAbstract.DbRepository[entities.ChoreStatusHistory],
	workflowPolicy *chorePolicies.WorkflowPolicy,
	chore *entities.Chore,
	now time.Time,
) (*entities.Chore, bool, error) {
	if !workflowPolicy.ApplyDeadline(chore, now) {
		return chore, false, nil
	}

	var failedChore entities.Chore
	err := choreRepository.Collection().FindOneAndUpdate(ctx, bson.M{
		"_id": chore.Id, "version": chore.Version,
		"dueDate": bson.M{"$lte": now}, "status": bson.M{"$ne": entities.Failed},
	}, bson.M{
		"$set": bson.M{
			"status": entities.Failed, "isCompleted": false,
			"completedBy": "", "completedAt": time.Time{},
		},
		"$inc": bson.M{"version": 1},
	}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&failedChore)
	if err == mongo.ErrNoDocuments {
		return nil, false, helpers.NewConflictError("chore.error.concurrent_update")
	}
	if err != nil {
		return nil, false, err
	}
	if err := addStatusHistory(
		ctx, statusHistoryRepository, failedChore.Id.Hex(), entities.Failed, systemUpdaterID, now,
	); err != nil {
		return nil, false, err
	}
	return &failedChore, true, nil
}

const systemUpdaterID = "system"
