package chore

import (
	"context"
	"sort"
	"time"

	chorePolicies "houseflowApi/internal/application/chore/policies"
	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// The deadline passes run as scheduled jobs: once a minute over open chores,
// and once a day over all of them. Each slot runs on one instance only.
const (
	DeadlineJobType         = "fauscrew:chore:deadline-reconcile"
	DeadlineFullScanJobType = "fauscrew:chore:deadline-full-scan"
)

const (
	deadlineScanInterval = time.Minute
	// deadlinePassTimeout stays below the interval; a failed minute is not
	// retried because the next one covers it.
	deadlinePassTimeout = 50 * time.Second
	fullScanInterval    = 24 * time.Hour
	fullScanTimeout     = 5 * time.Minute
	fullScanMaxRetry    = 2
	deadlineBatchSize   = 100
	deadlineUpdater     = "system"
)

type DeadlineService struct {
	chores   databaseAbstract.DbRepository[entities.Chore]
	history  databaseAbstract.DbRepository[entities.ChoreStatusHistory]
	workflow *chorePolicies.WorkflowPolicy
	notify   func(context.Context, string)
}

func NewDeadlineService(
	chores databaseAbstract.DbRepository[entities.Chore],
	history databaseAbstract.DbRepository[entities.ChoreStatusHistory],
	workflow *chorePolicies.WorkflowPolicy,
	notify func(context.Context, string),
) *DeadlineService {
	return &DeadlineService{chores: chores, history: history, workflow: workflow, notify: notify}
}

// Register binds and schedules the deadline passes; call it before jobs
// start. The full scan also covers legacy chores completed after their due
// date, which the open-status index does not reach, so it runs daily (and at
// the first start of a day) instead of every minute.
func (service *DeadlineService) Register(registry jobsAbstract.HandlerRegistry, scheduler jobsAbstract.PeriodicScheduler) {
	registry.Register(DeadlineJobType, service.RunOpenPass)
	registry.Register(DeadlineFullScanJobType, service.RunFullPass)
	scheduler.Every(DeadlineJobType, deadlineScanInterval,
		jobsAbstract.InQueue(jobsAbstract.QueueDefault),
		jobsAbstract.MaxRetry(0),
		jobsAbstract.Timeout(deadlinePassTimeout),
	)
	scheduler.Every(DeadlineFullScanJobType, fullScanInterval,
		jobsAbstract.InQueue(jobsAbstract.QueueLow),
		jobsAbstract.MaxRetry(fullScanMaxRetry),
		jobsAbstract.Timeout(fullScanTimeout),
	)
}

// RunOpenPass is the minute job: it fails open chores past their due date.
func (service *DeadlineService) RunOpenPass(ctx context.Context, _ []byte) error {
	return service.runPass(ctx, service.reconcileOpen)
}

// RunFullPass is the daily job; see Reconcile.
func (service *DeadlineService) RunFullPass(ctx context.Context, _ []byte) error {
	return service.runPass(ctx, service.Reconcile)
}

func (service *DeadlineService) runPass(
	ctx context.Context,
	reconcile func(context.Context, time.Time) ([]string, error),
) error {
	houseIDs, err := reconcile(ctx, time.Now())
	if err != nil {
		return err
	}
	if service.notify == nil {
		return nil
	}
	for _, houseID := range houseIDs {
		service.notify(ctx, houseID)
	}
	return nil
}

// Reconcile marks every currently overdue chore as failed, including legacy
// chores completed after their due date, and returns the affected house IDs.
// It is public so the deadline behavior can be tested without the job queue.
func (service *DeadlineService) Reconcile(ctx context.Context, now time.Time) ([]string, error) {
	return service.reconcile(ctx, now, deadlineCandidateFilter)
}

// reconcileOpen only looks at chores that are still open, which the
// {status, dueDate, _id} index serves without a scan or blocking sort.
func (service *DeadlineService) reconcileOpen(ctx context.Context, now time.Time) ([]string, error) {
	return service.reconcile(ctx, now, openDeadlineCandidateFilter)
}

func (service *DeadlineService) reconcile(
	ctx context.Context,
	now time.Time,
	candidateFilter func(time.Time) bson.M,
) ([]string, error) {
	affectedHouses := make(map[string]struct{})
	for {
		candidates, err := service.chores.FindManyByFilter(
			ctx,
			candidateFilter(now),
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

func openDeadlineCandidateFilter(now time.Time) bson.M {
	return bson.M{
		"status":  bson.M{"$in": bson.A{entities.Draft, entities.Progress, entities.InTest}},
		"dueDate": bson.M{"$lte": now},
	}
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
