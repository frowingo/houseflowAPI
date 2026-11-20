package jobs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
)

const (
	minPeriodicInterval   = time.Second
	periodicEnqueueBudget = 5 * time.Second
)

type periodicEntry struct {
	jobType  string
	interval time.Duration
	options  []jobsAbstract.Option
}

// scheduler enqueues each periodic job at the start of every slot. It runs
// in every instance: the slot's TaskID lets only the first enqueue of a slot
// through (Asynq keeps the ID until Retention, at least one interval, has
// passed), so a slot runs once however many instances schedule it.
type scheduler struct {
	queue jobsAbstract.JobQueue

	mutex   sync.Mutex
	entries []periodicEntry
	started bool
	stopped bool
	cancel  context.CancelFunc
	loops   sync.WaitGroup
}

func newScheduler(queue jobsAbstract.JobQueue) *scheduler {
	return &scheduler{queue: queue}
}

func (s *scheduler) Every(jobType string, interval time.Duration, options ...jobsAbstract.Option) {
	if err := jobsAbstract.ValidateType(jobType); err != nil {
		panic(err)
	}
	if interval < minPeriodicInterval {
		panic(fmt.Sprintf("jobs: %s interval %s is below %s", jobType, interval, minPeriodicInterval))
	}
	resolved, err := jobsAbstract.ResolveOptions(options...)
	if err != nil {
		panic(fmt.Sprintf("jobs: %s: %v", jobType, err))
	}
	if resolved.TaskID != "" || !resolved.ProcessAt.IsZero() {
		panic(fmt.Sprintf("jobs: %s: the scheduler sets TaskID and the run time itself", jobType))
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.started {
		panic(fmt.Sprintf("jobs: %s scheduled after job processing started", jobType))
	}
	for _, entry := range s.entries {
		if entry.jobType == jobType {
			panic(fmt.Sprintf("jobs: %s scheduled twice", jobType))
		}
	}
	s.entries = append(s.entries, periodicEntry{jobType: jobType, interval: interval, options: options})
}

// start checks every scheduled job has a handler, then starts the loops.
func (s *scheduler) start(registry *registry) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.started {
		return nil
	}
	for _, entry := range s.entries {
		if _, ok := registry.lookup(entry.jobType); !ok {
			return fmt.Errorf("scheduled job %s: %w", entry.jobType, jobsAbstract.ErrUnknownType)
		}
	}
	s.started = true
	if s.stopped {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for _, entry := range s.entries {
		s.loops.Add(1)
		go s.loop(ctx, entry)
	}
	return nil
}

// stop ends scheduling; a slot already enqueued still runs.
func (s *scheduler) stop() {
	s.mutex.Lock()
	s.stopped = true
	cancel := s.cancel
	s.mutex.Unlock()
	if cancel != nil {
		cancel()
	}
	s.loops.Wait()
}

func (s *scheduler) loop(ctx context.Context, entry periodicEntry) {
	defer s.loops.Done()
	var last time.Time
	for {
		slot := time.Now().Truncate(entry.interval)
		// A timer may wake a hair before the boundary on the wall clock.
		if !last.IsZero() && !slot.After(last) {
			slot = last.Add(entry.interval)
		}
		s.enqueue(ctx, entry, slot)
		last = slot
		timer := time.NewTimer(time.Until(slot.Add(entry.interval)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *scheduler) enqueue(ctx context.Context, entry periodicEntry, slot time.Time) {
	job, err := jobsAbstract.NewJSONJob(entry.jobType, jobsAbstract.PeriodicPayload{Slot: slot.UTC()})
	if err != nil {
		log.Printf("scheduled job %s: %v", entry.jobType, err)
		return
	}
	retention := entry.interval
	if resolved, err := jobsAbstract.ResolveOptions(entry.options...); err == nil && resolved.Retention > retention {
		retention = resolved.Retention
	}
	options := append(append([]jobsAbstract.Option(nil), entry.options...),
		jobsAbstract.TaskID(PeriodicTaskID(entry.jobType, slot)),
		jobsAbstract.Retention(retention),
	)
	ctx, cancel := context.WithTimeout(ctx, periodicEnqueueBudget)
	defer cancel()
	err = s.queue.Enqueue(ctx, job, options...)
	if err != nil && !errors.Is(err, jobsAbstract.ErrDuplicate) && ctx.Err() == nil {
		// The next slot tries again.
		log.Printf("scheduled job %s for %s was not queued: %v", entry.jobType, slot.UTC().Format(time.RFC3339), err)
	}
}

// PeriodicTaskID is the TaskID of a periodic job's slot.
func PeriodicTaskID(jobType string, slot time.Time) string {
	return jobsAbstract.TypePrefix + "periodic:" + strings.TrimPrefix(jobType, jobsAbstract.TypePrefix) +
		":" + strconv.FormatInt(slot.Unix(), 10)
}
