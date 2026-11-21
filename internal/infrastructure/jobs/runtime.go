// Package jobs runs background jobs behind the application's job port: Asynq
// on Valkey when REDIS_URL is set, an in-process fallback otherwise.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
)

// Runtime is what the composition root holds: the port for producers and
// handlers plus the lifecycle. Handlers are registered before Start.
type Runtime interface {
	jobsAbstract.JobQueue
	jobsAbstract.HandlerRegistry
	jobsAbstract.PeriodicScheduler
	// Start begins processing and scheduling. It does not wait for Valkey: a worker keeps
	// polling while Valkey is unreachable and resumes when it is back.
	Start(ctx context.Context) error
	// Stop stops scheduling and picking up new jobs; running ones continue
	// until Close.
	Stop()
	// Close waits for running jobs (bounded by ctx) and releases resources.
	Close(ctx context.Context) error
	Ping(ctx context.Context) error
	// Stats samples the queues and this instance's job runs for telemetry.
	Stats(ctx context.Context) (Stats, error)
}

const (
	defaultMaxRetry = 8
	defaultTimeout  = 30 * time.Second

	retryBaseDelay  = 5 * time.Second
	retryMaxDelay   = 30 * time.Minute
	retryAfterFloor = time.Second
	retryAfterCeil  = time.Hour
)

var errClosed = errors.New("job runtime is closed")

// queueWeights: a worker picks critical six times as often as low, but never
// starves a queue (no strict priority).
func queueWeights() map[string]int {
	return map[string]int{
		string(jobsAbstract.QueueCritical): 6,
		string(jobsAbstract.QueueDefault):  3,
		string(jobsAbstract.QueueLow):      1,
	}
}

// retryDelay is exponential from 5s, capped at 30m, with up to 20% jitter so
// jobs that failed together do not retry together. With the default 8
// retries a job gives up after roughly 21 minutes. A RetryAfterError wins.
func retryDelay(retried int, err error) time.Duration {
	var retryAfter *jobsAbstract.RetryAfterError
	if errors.As(err, &retryAfter) {
		return min(max(retryAfter.Delay, retryAfterFloor), retryAfterCeil)
	}
	delay := retryMaxDelay
	if retried >= 0 && retried < 20 {
		delay = min(retryBaseDelay<<retried, retryMaxDelay)
	}
	return delay + time.Duration(rand.Int64N(int64(delay)/5+1))
}

// prepare checks a job before it is accepted.
func prepare(registry *registry, job jobsAbstract.Job, options []jobsAbstract.Option) (jobsAbstract.Handler, jobsAbstract.EnqueueOptions, error) {
	if err := jobsAbstract.ValidateType(job.Type); err != nil {
		return nil, jobsAbstract.EnqueueOptions{}, err
	}
	resolved, err := jobsAbstract.ResolveOptions(options...)
	if err != nil {
		return nil, resolved, fmt.Errorf("enqueue %s: %w", job.Type, err)
	}
	// A typo in a job type would otherwise sit in the queue until it is
	// archived; producers and handlers are wired in the same binary.
	handler, ok := registry.lookup(job.Type)
	if !ok {
		return nil, resolved, fmt.Errorf("enqueue %s: %w", job.Type, jobsAbstract.ErrUnknownType)
	}
	return handler, resolved, nil
}
