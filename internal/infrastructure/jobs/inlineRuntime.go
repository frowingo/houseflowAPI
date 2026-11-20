package jobs

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
)

// InlineRuntime runs jobs in this process when no Valkey is configured
// (local development, tests). Jobs run once, in the background, and are lost
// on restart: no retries, no persistence. Delayed jobs wait on a timer.
type InlineRuntime struct {
	registry  *registry
	scheduler *scheduler
	baseCtx   context.Context
	cancel    context.CancelFunc

	mutex   sync.Mutex
	closed  bool
	nextID  uint64
	timers  map[uint64]*time.Timer
	taskIDs map[string]struct{}
	running sync.WaitGroup
	active  int

	counters *jobCounters
}

var _ Runtime = (*InlineRuntime)(nil)

func NewInlineRuntime() *InlineRuntime {
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &InlineRuntime{
		registry: newRegistry(),
		baseCtx:  ctx,
		cancel:   cancel,
		timers:   make(map[uint64]*time.Timer),
		taskIDs:  make(map[string]struct{}),
		counters: newJobCounters(),
	}
	runtime.scheduler = newScheduler(runtime)
	return runtime
}

func (r *InlineRuntime) Every(jobType string, interval time.Duration, options ...jobsAbstract.Option) {
	r.scheduler.Every(jobType, interval, options...)
}

func (r *InlineRuntime) Register(jobType string, handler jobsAbstract.Handler) {
	r.registry.Register(jobType, handler)
}

func (r *InlineRuntime) Enqueue(_ context.Context, job jobsAbstract.Job, options ...jobsAbstract.Option) error {
	handler, resolved, err := prepare(r.registry, job, options)
	if err != nil {
		return err
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.closed {
		return fmt.Errorf("enqueue %s: %w", job.Type, errClosed)
	}
	if resolved.TaskID != "" {
		if _, exists := r.taskIDs[resolved.TaskID]; exists {
			return fmt.Errorf("enqueue %s: %w", job.Type, jobsAbstract.ErrDuplicate)
		}
		r.taskIDs[resolved.TaskID] = struct{}{}
	}
	r.nextID++
	id := r.nextID
	// run takes the mutex first, so the timer is stored before it can fire.
	r.timers[id] = time.AfterFunc(max(time.Until(resolved.ProcessAt), 0), func() {
		r.run(id, job, handler, resolved)
	})
	return nil
}

func (r *InlineRuntime) run(id uint64, job jobsAbstract.Job, handler jobsAbstract.Handler, options jobsAbstract.EnqueueOptions) {
	r.mutex.Lock()
	if _, pending := r.timers[id]; !pending {
		r.mutex.Unlock() // dropped by Close
		return
	}
	delete(r.timers, id)
	r.running.Add(1)
	r.active++
	r.mutex.Unlock()

	defer r.running.Done()
	defer func() {
		r.mutex.Lock()
		r.active--
		r.mutex.Unlock()
	}()
	defer r.release(options.TaskID)
	started := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			r.counters.observe(job.Type, time.Since(started), fmt.Errorf("panic: %v", recovered))
			log.Printf("job %s panicked (in-process, not retried): %v", job.Type, recovered)
		}
	}()

	ctx, cancel := context.WithTimeout(r.baseCtx, valueOr(options.Timeout, defaultTimeout))
	defer cancel()
	ctx = jobsAbstract.ContextWithAttempt(ctx, jobsAbstract.Attempt{JobID: strconv.FormatUint(id, 10)})
	err := handler(ctx, job.Payload)
	r.counters.observe(job.Type, time.Since(started), err)
	if err != nil {
		log.Printf("job %s failed (in-process, not retried): %v", job.Type, err)
	}
}

func (r *InlineRuntime) Stats(context.Context) (Stats, error) {
	r.mutex.Lock()
	inline := InlineStats{Delayed: len(r.timers), Running: r.active}
	r.mutex.Unlock()
	return Stats{Backend: "inline", Jobs: r.counters.snapshot(), Inline: &inline}, nil
}

func (r *InlineRuntime) release(taskID string) {
	if taskID == "" {
		return
	}
	r.mutex.Lock()
	delete(r.taskIDs, taskID)
	r.mutex.Unlock()
}

func (r *InlineRuntime) Start(context.Context) error {
	r.registry.seal()
	return r.scheduler.start(r.registry)
}

func (r *InlineRuntime) Stop() { r.scheduler.stop() }

// Close drops jobs still waiting for their time and waits for running ones;
// when ctx ends first their contexts are canceled.
func (r *InlineRuntime) Close(ctx context.Context) error {
	r.scheduler.stop()
	r.mutex.Lock()
	r.closed = true
	dropped := 0
	for id, timer := range r.timers {
		timer.Stop()
		delete(r.timers, id)
		dropped++
	}
	r.mutex.Unlock()
	if dropped > 0 {
		log.Printf("in-process job runtime dropped %d delayed job(s) on shutdown", dropped)
	}

	done := make(chan struct{})
	go func() {
		r.running.Wait()
		close(done)
	}()
	select {
	case <-done:
		r.cancel()
		return nil
	case <-ctx.Done():
		r.cancel()
		return fmt.Errorf("wait for running jobs: %w", ctx.Err())
	}
}

func (r *InlineRuntime) Ping(context.Context) error { return nil }
