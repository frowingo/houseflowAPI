// Package abstract is the background job port. Application code enqueues
// jobs and registers handlers through it; the queue behind it (Asynq on
// Valkey, or the in-process fallback) lives in infrastructure/jobs.
package abstract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TypePrefix starts every job type name: "fauscrew:<area>:<action>".
const TypePrefix = "fauscrew:"

// Queue is a named job queue. Workers serve the queues below by weight, so a
// busy low queue cannot starve critical jobs.
type Queue string

const (
	QueueCritical Queue = "fauscrew-critical"
	QueueDefault  Queue = "fauscrew-default"
	QueueLow      Queue = "fauscrew-low"
)

// Queues lists every queue a worker serves. A job sent to any other queue
// would never run, so Enqueue rejects it.
var Queues = []Queue{QueueCritical, QueueDefault, QueueLow}

var (
	// ErrDuplicate means a job with the same TaskID is already queued, or
	// finished within its retention window. Callers that use TaskID for
	// deduplication treat it as success.
	ErrDuplicate = errors.New("job is already enqueued")
	// ErrPermanent marks a handler failure that retrying cannot fix (bad
	// payload, missing record). Wrap it and the job is not retried.
	ErrPermanent = errors.New("job failed permanently")
	// ErrUnknownType means no handler is registered for the job type.
	ErrUnknownType = errors.New("job type has no registered handler")
)

// Job is one unit of background work. Payload is opaque to the queue; the
// JSON helpers below cover the common case. Payloads are never logged.
type Job struct {
	Type    string
	Payload []byte
}

// NewJSONJob encodes payload as the job's JSON payload.
func NewJSONJob(jobType string, payload any) (Job, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Job{}, fmt.Errorf("encode %s payload: %w", jobType, err)
	}
	return Job{Type: jobType, Payload: encoded}, nil
}

// DecodeJSON decodes a handler's payload. A payload that cannot be decoded
// will not decode on a retry either, so the error is permanent.
func DecodeJSON[T any](payload []byte) (T, error) {
	var value T
	if err := json.Unmarshal(payload, &value); err != nil {
		return value, fmt.Errorf("decode job payload: %v: %w", err, ErrPermanent)
	}
	return value, nil
}

// ValidateType checks the "fauscrew:<area>:<action>" naming rule.
func ValidateType(jobType string) error {
	name, ok := strings.CutPrefix(jobType, TypePrefix)
	if !ok || name == "" || strings.ContainsAny(jobType, " \t\n") {
		return fmt.Errorf("job type %q must look like %s<area>:<action>", jobType, TypePrefix)
	}
	return nil
}

// Handler runs one job. Jobs are delivered at least once: a worker that
// stops mid-job hands it back to the queue, so handlers must be idempotent.
// Returning an error retries the job with backoff, unless it wraps
// ErrPermanent; RetryAfter picks the delay.
type Handler func(ctx context.Context, payload []byte) error

// HandlerRegistry binds job types to handlers. Registration happens at
// startup, before job processing starts; registering a type twice panics.
type HandlerRegistry interface {
	Register(jobType string, handler Handler)
}

// JobQueue accepts jobs for background processing. A nil error means the job
// is stored and will run; it says nothing about the outcome.
type JobQueue interface {
	Enqueue(ctx context.Context, job Job, options ...Option) error
}

// PeriodicScheduler enqueues a job once per interval. Slots are aligned to
// the interval (a minute job runs at every whole minute, a daily one at UTC
// midnight) and to the first slot when processing starts, so a restart
// catches up at once. Every instance schedules; a slot's TaskID makes one
// job run per slot across all of them. Jobs are registered before Start;
// the scheduler sets TaskID and the payload (PeriodicPayload) itself.
type PeriodicScheduler interface {
	Every(jobType string, interval time.Duration, options ...Option)
}

// PeriodicPayload is the payload of a scheduled job: the start of its slot.
type PeriodicPayload struct {
	Slot time.Time `json:"slot"`
}

// EnqueueOptions is the resolved form of the Option values.
type EnqueueOptions struct {
	Queue Queue
	// MaxRetry is the number of retries after the first attempt; nil uses
	// the queue default.
	MaxRetry *int
	// ProcessAt delays the job; zero runs it as soon as a worker is free.
	ProcessAt time.Time
	// Timeout bounds one attempt; zero uses the default.
	Timeout time.Duration
	// TaskID deduplicates: while a job with this ID is queued or retained,
	// Enqueue returns ErrDuplicate. Prefix it with TypePrefix.
	TaskID string
	// Retention keeps a finished job (and its TaskID) for this long.
	Retention time.Duration
}

type Option func(*EnqueueOptions)

func InQueue(queue Queue) Option {
	return func(options *EnqueueOptions) { options.Queue = queue }
}

func MaxRetry(retries int) Option {
	return func(options *EnqueueOptions) { options.MaxRetry = &retries }
}

func ProcessIn(delay time.Duration) Option {
	return func(options *EnqueueOptions) { options.ProcessAt = time.Now().Add(delay) }
}

func ProcessAt(at time.Time) Option {
	return func(options *EnqueueOptions) { options.ProcessAt = at }
}

func Timeout(timeout time.Duration) Option {
	return func(options *EnqueueOptions) { options.Timeout = timeout }
}

func TaskID(id string) Option {
	return func(options *EnqueueOptions) { options.TaskID = id }
}

func Retention(retention time.Duration) Option {
	return func(options *EnqueueOptions) { options.Retention = retention }
}

// ResolveOptions applies options over the defaults and validates the result.
func ResolveOptions(options ...Option) (EnqueueOptions, error) {
	resolved := EnqueueOptions{Queue: QueueDefault}
	for _, option := range options {
		option(&resolved)
	}
	if !knownQueue(resolved.Queue) {
		return resolved, fmt.Errorf("unknown job queue %q", resolved.Queue)
	}
	if resolved.MaxRetry != nil && *resolved.MaxRetry < 0 {
		return resolved, errors.New("job MaxRetry must not be negative")
	}
	if resolved.Timeout < 0 || resolved.Retention < 0 {
		return resolved, errors.New("job Timeout and Retention must not be negative")
	}
	if resolved.TaskID != "" && !strings.HasPrefix(resolved.TaskID, TypePrefix) {
		return resolved, fmt.Errorf("job TaskID %q must start with %s", resolved.TaskID, TypePrefix)
	}
	return resolved, nil
}

func knownQueue(queue Queue) bool {
	for _, known := range Queues {
		if queue == known {
			return true
		}
	}
	return false
}

// RetryAfterError asks for the next attempt after Delay, for example when a
// provider answered 429 with Retry-After.
type RetryAfterError struct {
	Delay time.Duration
	Err   error
}

func (e *RetryAfterError) Error() string {
	return fmt.Sprintf("retry after %s: %v", e.Delay, e.Err)
}

func (e *RetryAfterError) Unwrap() error { return e.Err }

func RetryAfter(delay time.Duration, err error) error {
	return &RetryAfterError{Delay: delay, Err: err}
}

// Attempt describes the current delivery of a job to its handler.
type Attempt struct {
	JobID    string
	Retried  int
	MaxRetry int
}

// Last reports whether a failure now ends the job.
func (attempt Attempt) Last() bool { return attempt.Retried >= attempt.MaxRetry }

type attemptKey struct{}

func ContextWithAttempt(ctx context.Context, attempt Attempt) context.Context {
	return context.WithValue(ctx, attemptKey{}, attempt)
}

// AttemptFromContext returns the delivery a handler is running for.
func AttemptFromContext(ctx context.Context) (Attempt, bool) {
	attempt, ok := ctx.Value(attemptKey{}).(Attempt)
	return attempt, ok
}
