package jobs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

const (
	defaultConcurrency              = 10
	defaultShutdownTimeout          = 8 * time.Second
	defaultDelayedTaskCheckInterval = 5 * time.Second
	// Enqueue runs inside requests; an unreachable Valkey must fail fast.
	enqueueTimeout = 3 * time.Second
)

type AsynqOptions struct {
	URL string
	// Consume starts a worker in this process. Without it the process only
	// enqueues (FAUSCREW_ROLE=api).
	Consume     bool
	Concurrency int
	// ShutdownTimeout bounds how long Close waits for running jobs; jobs
	// still running then go back to the queue and run again elsewhere.
	ShutdownTimeout time.Duration
	// DelayedTaskCheckInterval is how often delayed and retrying jobs are
	// moved to their queue, i.e. the timing granularity of ProcessAt.
	DelayedTaskCheckInterval time.Duration
}

// AsynqRuntime keeps jobs in Valkey: they survive restarts and deploys, are
// retried with backoff, and run on whichever instance is free.
type AsynqRuntime struct {
	redis     *redis.Client
	client    *asynq.Client
	server    *asynq.Server
	inspector *asynq.Inspector
	registry  *registry
	scheduler *scheduler
	counters  *jobCounters
}

var _ Runtime = (*AsynqRuntime)(nil)

func NewAsynqRuntime(options AsynqOptions) (*AsynqRuntime, error) {
	if options.URL == "" {
		return nil, errors.New("redis URL is required")
	}
	redisOptions, err := redis.ParseURL(options.URL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	redisOptions.MaxRetries = 2
	redisOptions.DialTimeout = 3 * time.Second
	redisOptions.ReadTimeout = 3 * time.Second
	redisOptions.WriteTimeout = 3 * time.Second
	redisClient := redis.NewClient(redisOptions)

	runtime := &AsynqRuntime{
		redis:  redisClient,
		client: asynq.NewClientFromRedisClient(redisClient),
		// Shares the connection; it must not be closed on its own.
		inspector: asynq.NewInspectorFromRedisClient(redisClient),
		registry:  newRegistry(),
		counters:  newJobCounters(),
	}
	runtime.scheduler = newScheduler(runtime)
	if options.Consume {
		runtime.server = asynq.NewServerFromRedisClient(redisClient, asynq.Config{
			Concurrency:              valueOr(options.Concurrency, defaultConcurrency),
			Queues:                   queueWeights(),
			RetryDelayFunc:           func(retried int, err error, _ *asynq.Task) time.Duration { return retryDelay(retried, err) },
			ErrorHandler:             asynq.ErrorHandlerFunc(logFailure),
			ShutdownTimeout:          valueOr(options.ShutdownTimeout, defaultShutdownTimeout),
			DelayedTaskCheckInterval: valueOr(options.DelayedTaskCheckInterval, defaultDelayedTaskCheckInterval),
			LogLevel:                 asynq.WarnLevel,
		})
	}
	return runtime, nil
}

func (r *AsynqRuntime) Register(jobType string, handler jobsAbstract.Handler) {
	r.registry.Register(jobType, handler)
}

func (r *AsynqRuntime) Every(jobType string, interval time.Duration, options ...jobsAbstract.Option) {
	r.scheduler.Every(jobType, interval, options...)
}

func (r *AsynqRuntime) Enqueue(ctx context.Context, job jobsAbstract.Job, options ...jobsAbstract.Option) error {
	_, resolved, err := prepare(r.registry, job, options)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, enqueueTimeout)
	defer cancel()
	_, err = r.client.EnqueueContext(ctx, asynq.NewTask(job.Type, job.Payload), asynqOptions(resolved)...)
	if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
		return fmt.Errorf("enqueue %s: %w", job.Type, jobsAbstract.ErrDuplicate)
	}
	if err != nil {
		return fmt.Errorf("enqueue %s: %w", job.Type, err)
	}
	return nil
}

func (r *AsynqRuntime) Start(context.Context) error {
	r.registry.seal()
	if r.server != nil {
		if err := r.server.Start(asynq.HandlerFunc(r.process)); err != nil {
			return err
		}
	}
	// An api-only instance schedules too; a worker runs the slot.
	return r.scheduler.start(r.registry)
}

func (r *AsynqRuntime) Stop() {
	r.scheduler.stop()
	if r.server != nil {
		r.server.Stop()
	}
}

func (r *AsynqRuntime) Close(ctx context.Context) error {
	r.scheduler.stop()
	if r.server != nil {
		done := make(chan struct{})
		go func() {
			r.server.Shutdown()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			return fmt.Errorf("wait for running jobs: %w", ctx.Err())
		}
	}
	// The asynq client shares this connection and must not close it itself.
	return r.redis.Close()
}

func (r *AsynqRuntime) Ping(ctx context.Context) error {
	return r.redis.Ping(ctx).Err()
}

// Stats reads the queues the cluster has used so far (a queue appears with
// its first job) and Valkey's memory.
func (r *AsynqRuntime) Stats(ctx context.Context) (Stats, error) {
	stats := Stats{Backend: "asynq", Jobs: r.counters.snapshot()}
	existing, err := r.inspector.Queues()
	if err != nil {
		return stats, fmt.Errorf("list queues: %w", err)
	}
	for _, name := range knownQueueNames() {
		if !slices.Contains(existing, name) {
			continue
		}
		info, err := r.inspector.GetQueueInfo(name)
		if err != nil {
			return stats, fmt.Errorf("queue %s: %w", name, err)
		}
		stats.Queues = append(stats.Queues, QueueStats{
			Queue: name, Pending: info.Pending, Active: info.Active, Scheduled: info.Scheduled,
			Retry: info.Retry, Archived: info.Archived, Completed: info.Completed,
			LatencyMs: info.Latency.Milliseconds(), ProcessedToday: info.Processed, FailedToday: info.Failed,
			Paused: info.Paused,
		})
	}
	ctx, cancel := context.WithTimeout(ctx, enqueueTimeout)
	defer cancel()
	info, err := r.redis.Info(ctx, "memory").Result()
	if err != nil {
		return stats, fmt.Errorf("valkey memory: %w", err)
	}
	stats.Valkey = parseValkeyMemory(info)
	return stats, nil
}

func (r *AsynqRuntime) process(ctx context.Context, task *asynq.Task) error {
	handler, ok := r.registry.lookup(task.Type())
	if !ok {
		// Retried: during a rolling deploy another instance may know it.
		return fmt.Errorf("%s: %w", task.Type(), jobsAbstract.ErrUnknownType)
	}
	started := time.Now()
	defer func() {
		// Counted, then handed back: Asynq turns a panic into a failed attempt.
		if recovered := recover(); recovered != nil {
			r.counters.observe(task.Type(), time.Since(started), fmt.Errorf("panic: %v", recovered))
			panic(recovered)
		}
	}()
	err := handler(jobsAbstract.ContextWithAttempt(ctx, attemptOf(ctx)), task.Payload())
	r.counters.observe(task.Type(), time.Since(started), err)
	if err != nil && errors.Is(err, jobsAbstract.ErrPermanent) {
		return fmt.Errorf("%w (%w)", err, asynq.SkipRetry)
	}
	return err
}

func attemptOf(ctx context.Context) jobsAbstract.Attempt {
	id, _ := asynq.GetTaskID(ctx)
	retried, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	return jobsAbstract.Attempt{JobID: id, Retried: retried, MaxRetry: maxRetry}
}

// logFailure logs the job type and attempt, never the payload: payloads can
// hold verification codes and push tokens.
func logFailure(ctx context.Context, task *asynq.Task, err error) {
	attempt := attemptOf(ctx)
	outcome := "will retry"
	if attempt.Last() || errors.Is(err, asynq.SkipRetry) {
		outcome = "archived"
	}
	log.Printf("job %s failed on attempt %d of %d (%s): %v",
		task.Type(), attempt.Retried+1, attempt.MaxRetry+1, outcome, err)
}

func asynqOptions(options jobsAbstract.EnqueueOptions) []asynq.Option {
	maxRetry := defaultMaxRetry
	if options.MaxRetry != nil {
		maxRetry = *options.MaxRetry
	}
	result := []asynq.Option{
		asynq.Queue(string(options.Queue)),
		asynq.MaxRetry(maxRetry),
		asynq.Timeout(valueOr(options.Timeout, defaultTimeout)),
	}
	if !options.ProcessAt.IsZero() {
		result = append(result, asynq.ProcessAt(options.ProcessAt))
	}
	if options.TaskID != "" {
		result = append(result, asynq.TaskID(options.TaskID))
	}
	if options.Retention > 0 {
		result = append(result, asynq.Retention(options.Retention))
	}
	return result
}

func valueOr[T comparable](value, fallback T) T {
	var zero T
	if value == zero {
		return fallback
	}
	return value
}
