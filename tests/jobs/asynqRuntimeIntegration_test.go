package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
	"houseflowApi/tests/testkit"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

const waitTimeout = 10 * time.Second

func newRuntime(t *testing.T, redisURL string, consume bool, register func(infrastructureJobs.Runtime)) infrastructureJobs.Runtime {
	t.Helper()
	runtime, err := infrastructureJobs.NewAsynqRuntime(infrastructureJobs.AsynqOptions{
		URL:                      redisURL,
		Consume:                  consume,
		ShutdownTimeout:          2 * time.Second,
		DelayedTaskCheckInterval: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if register != nil {
		register(runtime)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = runtime.Close(ctx)
	})
	return runtime
}

func newInspector(t *testing.T, redisURL string) *asynq.Inspector {
	t.Helper()
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	return asynq.NewInspectorFromRedisClient(client)
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestAsynqRuntimeProcessesJobWithAttempt(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	received := make(chan jobsAbstract.Attempt, 1)
	runtime := newRuntime(t, redisURL, true, func(runtime infrastructureJobs.Runtime) {
		runtime.Register("fauscrew:test:echo", func(ctx context.Context, payload []byte) error {
			attempt, _ := jobsAbstract.AttemptFromContext(ctx)
			if string(payload) == `{"value":7}` {
				received <- attempt
			}
			return nil
		})
	})
	job, err := jobsAbstract.NewJSONJob("fauscrew:test:echo", map[string]int{"value": 7})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(context.Background(), job, jobsAbstract.InQueue(jobsAbstract.QueueCritical), jobsAbstract.MaxRetry(3)); err != nil {
		t.Fatal(err)
	}
	select {
	case attempt := <-received:
		if attempt.JobID == "" || attempt.Retried != 0 || attempt.MaxRetry != 3 {
			t.Fatalf("attempt = %+v", attempt)
		}
	case <-time.After(waitTimeout):
		t.Fatal("job was not processed")
	}
	if err := runtime.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAsynqRuntimeRetriesAfterRequestedDelay(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	var attempts atomic.Int32
	var firstAt, lastAt atomic.Int64
	runtime := newRuntime(t, redisURL, true, func(runtime infrastructureJobs.Runtime) {
		runtime.Register("fauscrew:test:flaky", func(ctx context.Context, _ []byte) error {
			now := time.Now().UnixNano()
			if attempts.Add(1) == 1 {
				firstAt.Store(now)
				return jobsAbstract.RetryAfter(time.Second, errors.New("provider busy"))
			}
			lastAt.Store(now)
			return nil
		})
	})
	if err := runtime.Enqueue(context.Background(), jobsAbstract.Job{Type: "fauscrew:test:flaky"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the retry to succeed", func() bool { return attempts.Load() == 2 })
	if gap := time.Duration(lastAt.Load() - firstAt.Load()); gap < time.Second {
		t.Fatalf("retried after %v, want at least 1s", gap)
	}
}

func TestAsynqRuntimeArchivesPermanentFailureWithoutRetry(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	var attempts atomic.Int32
	runtime := newRuntime(t, redisURL, true, func(runtime infrastructureJobs.Runtime) {
		runtime.Register("fauscrew:test:invalid", func(context.Context, []byte) error {
			attempts.Add(1)
			return jobsAbstract.ErrPermanent
		})
	})
	if err := runtime.Enqueue(context.Background(), jobsAbstract.Job{Type: "fauscrew:test:invalid"},
		jobsAbstract.TaskID("fauscrew:test:invalid:1")); err != nil {
		t.Fatal(err)
	}
	inspector := newInspector(t, redisURL)
	waitFor(t, "the job to be archived", func() bool {
		info, err := inspector.GetTaskInfo(string(jobsAbstract.QueueDefault), "fauscrew:test:invalid:1")
		return err == nil && info.State == asynq.TaskStateArchived
	})
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", attempts.Load())
	}
}

func TestAsynqRuntimeRejectsDuplicateTaskIDAndUnknownJobs(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	runtime := newRuntime(t, redisURL, true, func(runtime infrastructureJobs.Runtime) {
		runtime.Register("fauscrew:test:later", func(context.Context, []byte) error { return nil })
	})
	ctx := context.Background()
	job := jobsAbstract.Job{Type: "fauscrew:test:later"}
	options := []jobsAbstract.Option{jobsAbstract.TaskID("fauscrew:test:later:1"), jobsAbstract.ProcessIn(time.Hour)}
	if err := runtime.Enqueue(ctx, job, options...); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(ctx, job, options...); !errors.Is(err, jobsAbstract.ErrDuplicate) {
		t.Fatalf("second enqueue = %v, want ErrDuplicate", err)
	}
	if err := runtime.Enqueue(ctx, jobsAbstract.Job{Type: "fauscrew:test:unknown"}); !errors.Is(err, jobsAbstract.ErrUnknownType) {
		t.Fatalf("unknown type enqueue = %v, want ErrUnknownType", err)
	}
	if err := runtime.Enqueue(ctx, job, jobsAbstract.InQueue("default")); err == nil {
		t.Fatal("enqueue to an unserved queue was accepted")
	}
}

// FAUSCREW_ROLE=api: the API instance only enqueues, a worker instance runs.
func TestEnqueueOnlyRuntimeLeavesJobsToWorkers(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	var apiRuns, workerRuns atomic.Int32
	api := newRuntime(t, redisURL, false, func(runtime infrastructureJobs.Runtime) {
		runtime.Register("fauscrew:test:split", func(context.Context, []byte) error {
			apiRuns.Add(1)
			return nil
		})
	})
	newRuntime(t, redisURL, true, func(runtime infrastructureJobs.Runtime) {
		runtime.Register("fauscrew:test:split", func(context.Context, []byte) error {
			workerRuns.Add(1)
			return nil
		})
	})
	for range 3 {
		if err := api.Enqueue(context.Background(), jobsAbstract.Job{Type: "fauscrew:test:split"}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "the worker to run every job", func() bool { return workerRuns.Load() == 3 })
	if apiRuns.Load() != 0 {
		t.Fatalf("enqueue-only runtime ran %d job(s)", apiRuns.Load())
	}
}

func TestAsynqRuntimeCloseWaitsForRunningJob(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	started := make(chan struct{})
	var finished atomic.Bool
	runtime, err := infrastructureJobs.NewAsynqRuntime(infrastructureJobs.AsynqOptions{
		URL: redisURL, Consume: true, ShutdownTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.Register("fauscrew:test:slow", func(context.Context, []byte) error {
		close(started)
		time.Sleep(500 * time.Millisecond)
		finished.Store(true)
		return nil
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(context.Background(), jobsAbstract.Job{Type: "fauscrew:test:slow"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(waitTimeout):
		t.Fatal("job did not start")
	}
	runtime.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !finished.Load() {
		t.Fatal("Close returned before the running job finished")
	}
}
