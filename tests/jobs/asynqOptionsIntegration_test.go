package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
	"houseflowApi/tests/testkit"

	"github.com/hibiken/asynq"
)

// Asynq stores times in whole seconds, so stored delays are compared with a
// one-second tolerance on each side.
func assertDuration(t *testing.T, name string, got, from, to time.Duration) {
	t.Helper()
	if got < from || got > to {
		t.Errorf("%s = %v, want %v..%v", name, got, from, to)
	}
}

func TestAsynqRuntimeStoresEnqueueOptions(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	// Enqueue only, so the jobs stay where the inspector can read them.
	runtime := newRuntime(t, redisURL, false, func(runtime infrastructureJobs.Runtime) {
		runtime.Register("fauscrew:test:options", func(context.Context, []byte) error { return nil })
	})
	ctx := context.Background()
	job := jobsAbstract.Job{Type: "fauscrew:test:options"}
	at := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := runtime.Enqueue(ctx, job,
		jobsAbstract.TaskID("fauscrew:test:options:custom"), jobsAbstract.InQueue(jobsAbstract.QueueCritical),
		jobsAbstract.MaxRetry(2), jobsAbstract.ProcessAt(at), jobsAbstract.Timeout(time.Minute),
		jobsAbstract.Retention(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(ctx, job, jobsAbstract.TaskID("fauscrew:test:options:default")); err != nil {
		t.Fatal(err)
	}

	inspector := newInspector(t, redisURL)
	custom, err := inspector.GetTaskInfo(string(jobsAbstract.QueueCritical), "fauscrew:test:options:custom")
	if err != nil {
		t.Fatal(err)
	}
	if custom.State != asynq.TaskStateScheduled || custom.MaxRetry != 2 || custom.Timeout != time.Minute ||
		custom.Retention != 10*time.Minute || !custom.NextProcessAt.Equal(at) {
		t.Errorf("custom job = state %v, maxRetry %d, timeout %v, retention %v, next %v (want %v)",
			custom.State, custom.MaxRetry, custom.Timeout, custom.Retention, custom.NextProcessAt, at)
	}

	defaults, err := inspector.GetTaskInfo(string(jobsAbstract.QueueDefault), "fauscrew:test:options:default")
	if err != nil {
		t.Fatal(err)
	}
	if defaults.State != asynq.TaskStatePending || defaults.MaxRetry != 8 || defaults.Timeout != 30*time.Second ||
		defaults.Retention != 0 {
		t.Errorf("default job = state %v, maxRetry %d, timeout %v, retention %v",
			defaults.State, defaults.MaxRetry, defaults.Timeout, defaults.Retention)
	}
}

// The first retry waits 5s plus up to 20% jitter; RetryAfter delays are
// clamped to 1s..1h.
func TestAsynqRuntimeRetryDelays(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	runtime, err := infrastructureJobs.NewAsynqRuntime(infrastructureJobs.AsynqOptions{
		URL: redisURL, Consume: true, ShutdownTimeout: 2 * time.Second,
		// Long enough that failed jobs stay in the retry set while inspected.
		DelayedTaskCheckInterval: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.Register("fauscrew:test:fail", func(_ context.Context, payload []byte) error {
		switch string(payload) {
		case "short":
			return jobsAbstract.RetryAfter(time.Millisecond, errors.New("busy"))
		case "long":
			return jobsAbstract.RetryAfter(48*time.Hour, errors.New("busy"))
		default:
			return errors.New("busy")
		}
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = runtime.Close(ctx)
	})

	cases := []struct {
		payload  string
		from, to time.Duration
	}{
		{"backoff", 5 * time.Second, 7 * time.Second},
		{"short", time.Second, 2 * time.Second},
		{"long", time.Hour, time.Hour + time.Second},
	}
	for _, tc := range cases {
		if err := runtime.Enqueue(context.Background(), jobsAbstract.Job{Type: "fauscrew:test:fail", Payload: []byte(tc.payload)},
			jobsAbstract.TaskID("fauscrew:test:fail:"+tc.payload)); err != nil {
			t.Fatal(err)
		}
	}

	inspector := newInspector(t, redisURL)
	for _, tc := range cases {
		var info *asynq.TaskInfo
		waitFor(t, tc.payload+" to wait for a retry", func() bool {
			info, err = inspector.GetTaskInfo(string(jobsAbstract.QueueDefault), "fauscrew:test:fail:"+tc.payload)
			return err == nil && info.State == asynq.TaskStateRetry
		})
		assertDuration(t, tc.payload+" retry delay", info.NextProcessAt.Sub(info.LastFailedAt), tc.from, tc.to)
	}
}
