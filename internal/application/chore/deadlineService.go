package chore

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	chorePolicies "houseflowApi/internal/application/chore/policies"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	deadlineScanInterval = time.Minute
	deadlineBatchSize    = 100
	deadlineUpdater      = "system"
)

type DeadlineService struct {
	chores   databaseAbstract.DbRepository[entities.Chore]
	history  databaseAbstract.DbRepository[entities.ChoreStatusHistory]
	workflow *chorePolicies.WorkflowPolicy
	notify   func(context.Context, string)

	mutex  sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewDeadlineService(
	chores databaseAbstract.DbRepository[entities.Chore],
	history databaseAbstract.DbRepository[entities.ChoreStatusHistory],
	workflow *chorePolicies.WorkflowPolicy,
	notify func(context.Context, string),
) *DeadlineService {
	return &DeadlineService{chores: chores, history: history, workflow: workflow, notify: notify}
}

func (service *DeadlineService) Start(ctx context.Context) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if service.done != nil {
		return
	}
	workerCtx, cancel := context.WithCancel(ctx)
	service.cancel = cancel
	service.done = make(chan struct{})
	go service.run(workerCtx)
}

func (service *DeadlineService) run(ctx context.Context) {
	defer close(service.done)
	service.runOnce(ctx)
	ticker := time.NewTicker(deadlineScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			service.runOnce(ctx)
		}
	}
}

func (service *DeadlineService) runOnce(ctx context.Context) {
	houseIDs, err := service.Reconcile(ctx, time.Now())
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("chore deadline reconciliation failed: %v", err)
		}
		return
	}
	if service.notify == nil {
		return
	}
	for _, houseID := range houseIDs {
		service.notify(ctx, houseID)
	}
}

// Reconcile marks every currently overdue chore as failed and returns the
// affected house IDs. It is public so the deadline behavior can be tested
// without waiting for the background ticker.
func (service *DeadlineService) Reconcile(ctx context.Context, now time.Time) ([]string, error) {
	affectedHouses := make(map[string]struct{})
	for {
		candidates, err := service.chores.FindManyByFilter(
			ctx,
			deadlineCandidateFilter(now),
			options.Find().SetSort(bson.D{{Key: "dueDate", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(deadlineBatchSize),
		)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			break
		}

		updated := 0
		for index := range candidates {
			houseID, changed, err := service.failCandidate(ctx, candidates[index], now)
			if err != nil {
				return nil, err
			}
			if changed {
				updated++
				affectedHouses[houseID] = struct{}{}
			}
		}
		if updated == 0 || len(candidates) < deadlineBatchSize {
			break
		}
	}

	houseIDs := make([]string, 0, len(affectedHouses))
	for houseID := range affectedHouses {
		houseIDs = append(houseIDs, houseID)
	}
	sort.Strings(houseIDs)
	return houseIDs, nil
}

func (service *DeadlineService) failCandidate(
	ctx context.Context,
	candidate entities.Chore,
	now time.Time,
) (string, bool, error) {
	if !service.workflow.ApplyDeadline(&candidate, now) {
		return "", false, nil
	}
	var failedChore entities.Chore
	changed := false
	err := service.chores.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		changed = false
		filter := deadlineCandidateFilter(now)
		filter["_id"] = candidate.Id
		filter["version"] = candidate.Version
		err := service.chores.Collection().FindOneAndUpdate(txCtx, filter, bson.M{
			"$set": bson.M{
				"status": entities.Failed, "isCompleted": false,
				"completedBy": "", "completedAt": time.Time{},
			},
			"$inc": bson.M{"version": 1},
		}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&failedChore)
		if err == mongo.ErrNoDocuments {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := service.history.Insert(txCtx, entities.ChoreStatusHistory{
			ChoreId: failedChore.Id.Hex(), Status: entities.Failed,
			DateTime: now, Updater: deadlineUpdater,
		}); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return "", false, err
	}
	if !changed {
		return "", false, nil
	}
	return failedChore.HouseId, true, nil
}

func deadlineCandidateFilter(now time.Time) bson.M {
	return bson.M{
		"dueDate": bson.M{"$lte": now},
		"$or": bson.A{
			bson.M{"status": bson.M{"$in": bson.A{entities.Draft, entities.Progress, entities.InTest}}},
			bson.M{
				"status": entities.Completed,
				"$expr":  bson.M{"$gte": bson.A{"$completedAt", "$dueDate"}},
			},
		},
	}
}

func (service *DeadlineService) Close(ctx context.Context) error {
	service.mutex.Lock()
	cancel, done := service.cancel, service.done
	service.mutex.Unlock()
	if cancel != nil {
		cancel()
	}
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
