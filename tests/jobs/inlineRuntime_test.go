package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
)

func TestRuntimeRejectsBadHandlerRegistrations(t *testing.T) {
	handler := func(context.Context, []byte) error { return nil }
	mustPanic := func(name string, register func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: did not panic", name)
			}
		}()
		register()
	}
	runtime := infrastructureJobs.NewInlineRuntime()
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	runtime.Register("fauscrew:test:ok", handler)
	mustPanic("duplicate", func() { runtime.Register("fauscrew:test:ok", handler) })
	mustPanic("bad name", func() { runtime.Register("test:ok", handler) })
	mustPanic("nil handler", func() { runtime.Register("fauscrew:test:nil", nil) })
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustPanic("after start", func() { runtime.Register("fauscrew:test:late", handler) })
}

func TestInlineRuntimeRunsJobsInBackground(t *testing.T) {
	runtime := infrastructureJobs.NewInlineRuntime()
	done := make(chan jobsAbstract.Attempt, 1)
	runtime.Register("fauscrew:test:run", func(ctx context.Context, payload []byte) error {
		attempt, _ := jobsAbstract.AttemptFromContext(ctx)
		if string(payload) == "hello" {
			done <- attempt
		}
		return nil
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(context.Background(), jobsAbstract.Job{Type: "fauscrew:test:run", Payload: []byte("hello")}); err != nil {
		t.Fatal(err)
	}
	select {
	case attempt := <-done:
		if attempt.JobID == "" {
			t.Error("attempt has no job ID")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("job did not run")
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestInlineRuntimeRejectsUnknownTypesDuplicateTaskIDsAndClosedEnqueue(t *testing.T) {
	runtime := infrastructureJobs.NewInlineRuntime()
	release := make(chan struct{})
	runtime.Register("fauscrew:test:block", func(context.Context, []byte) error {
		<-release
		return nil
	})
	ctx := context.Background()
	job := jobsAbstract.Job{Type: "fauscrew:test:block"}

	if err := runtime.Enqueue(ctx, jobsAbstract.Job{Type: "fauscrew:test:missing"}); !errors.Is(err, jobsAbstract.ErrUnknownType) {
		t.Errorf("unknown type error = %v", err)
	}
	if err := runtime.Enqueue(ctx, job, jobsAbstract.TaskID("fauscrew:test:once")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(ctx, job, jobsAbstract.TaskID("fauscrew:test:once")); !errors.Is(err, jobsAbstract.ErrDuplicate) {
		t.Errorf("duplicate TaskID error = %v", err)
	}
	close(release)
	if err := runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(ctx, job); err == nil || errors.Is(err, jobsAbstract.ErrDuplicate) {
		t.Errorf("enqueue after close = %v, want a closed runtime error", err)
	}
}

func TestInlineRuntimeDelaysAndDropsPendingOnClose(t *testing.T) {
	runtime := infrastructureJobs.NewInlineRuntime()
	var lateRuns atomic.Int32
	runtime.Register("fauscrew:test:late", func(context.Context, []byte) error {
		lateRuns.Add(1)
		return nil
	})
	start := time.Now()
	ran := make(chan time.Duration, 1)
	runtime.Register("fauscrew:test:soon", func(context.Context, []byte) error {
		ran <- time.Since(start)
		return nil
	})
	ctx := context.Background()
	if err := runtime.Enqueue(ctx, jobsAbstract.Job{Type: "fauscrew:test:soon"}, jobsAbstract.ProcessIn(150*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(ctx, jobsAbstract.Job{Type: "fauscrew:test:late"}, jobsAbstract.ProcessIn(time.Hour)); err != nil {
		t.Fatal(err)
	}
	select {
	case elapsed := <-ran:
		if elapsed < 150*time.Millisecond {
			t.Errorf("delayed job ran after %v", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delayed job did not run")
	}
	if err := runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if lateRuns.Load() != 0 {
		t.Error("job scheduled an hour ahead ran")
	}
}

func TestInlineRuntimeSurvivesPanickingHandlerAndCancelsOnCloseTimeout(t *testing.T) {
	runtime := infrastructureJobs.NewInlineRuntime()
	canceled := make(chan struct{})
	runtime.Register("fauscrew:test:panic", func(context.Context, []byte) error { panic("boom") })
	runtime.Register("fauscrew:test:slow", func(ctx context.Context, _ []byte) error {
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	})
	ctx := context.Background()
	if err := runtime.Enqueue(ctx, jobsAbstract.Job{Type: "fauscrew:test:panic"}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(ctx, jobsAbstract.Job{Type: "fauscrew:test:slow"}, jobsAbstract.Timeout(time.Hour)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	closeCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := runtime.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want deadline exceeded", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("running job was not canceled")
	}
}
