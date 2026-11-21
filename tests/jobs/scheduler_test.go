package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
	"houseflowApi/tests/testkit"
)

// slotRecorder counts the runs of each slot of a periodic job.
type slotRecorder struct {
	mutex sync.Mutex
	runs  map[time.Time]int
	order []time.Time
}

func newSlotRecorder() *slotRecorder {
	return &slotRecorder{runs: map[time.Time]int{}}
}

func (r *slotRecorder) handler(_ context.Context, payload []byte) error {
	periodic, err := jobsAbstract.DecodeJSON[jobsAbstract.PeriodicPayload](payload)
	if err != nil {
		return err
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.runs[periodic.Slot] == 0 {
		r.order = append(r.order, periodic.Slot)
	}
	r.runs[periodic.Slot]++
	return nil
}

func (r *slotRecorder) snapshot() (map[time.Time]int, []time.Time) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	runs := make(map[time.Time]int, len(r.runs))
	for slot, count := range r.runs {
		runs[slot] = count
	}
	return runs, append([]time.Time(nil), r.order...)
}

func (r *slotRecorder) slots() int {
	_, order := r.snapshot()
	return len(order)
}

func TestSchedulerRunsAlignedSlotsFromStartUntilStop(t *testing.T) {
	recorder := newSlotRecorder()
	runtime := infrastructureJobs.NewInlineRuntime()
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	runtime.Register("fauscrew:test:tick", recorder.handler)
	runtime.Every("fauscrew:test:tick", time.Second)
	started := time.Now()
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "two slots", func() bool { return recorder.slots() >= 2 })
	runtime.Stop()
	stoppedAt := recorder.slots()

	runs, order := recorder.snapshot()
	if first := order[0]; !first.Equal(started.Truncate(time.Second)) && !first.Equal(started.Truncate(time.Second).Add(time.Second)) {
		t.Fatalf("first slot %s does not start the run started at %s", first, started)
	}
	for index, slot := range order {
		if !slot.Equal(slot.Truncate(time.Second)) || runs[slot] != 1 {
			t.Fatalf("slot %s: aligned? runs = %d", slot, runs[slot])
		}
		if index > 0 && !slot.Equal(order[index-1].Add(time.Second)) {
			t.Fatalf("slots %s and %s are not consecutive", order[index-1], slot)
		}
	}
	time.Sleep(1200 * time.Millisecond)
	if recorder.slots() != stoppedAt {
		t.Fatal("the scheduler kept enqueuing after Stop")
	}
}

func TestSchedulerRejectsBadSchedules(t *testing.T) {
	handler := func(context.Context, []byte) error { return nil }
	mustPanic := func(name string, schedule func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: did not panic", name)
			}
		}()
		schedule()
	}
	runtime := infrastructureJobs.NewInlineRuntime()
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	runtime.Register("fauscrew:test:ok", handler)
	runtime.Every("fauscrew:test:ok", time.Minute)
	mustPanic("twice", func() { runtime.Every("fauscrew:test:ok", time.Hour) })
	mustPanic("bad name", func() { runtime.Every("test:ok", time.Minute) })
	mustPanic("too often", func() { runtime.Every("fauscrew:test:fast", time.Millisecond) })
	mustPanic("own task id", func() {
		runtime.Every("fauscrew:test:id", time.Minute, jobsAbstract.TaskID("fauscrew:test:fixed"))
	})
	mustPanic("own run time", func() {
		runtime.Every("fauscrew:test:at", time.Minute, jobsAbstract.ProcessIn(time.Second))
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustPanic("after start", func() { runtime.Every("fauscrew:test:late", time.Minute) })
}

func TestSchedulerStartFailsWithoutHandler(t *testing.T) {
	runtime := infrastructureJobs.NewInlineRuntime()
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	runtime.Every("fauscrew:test:orphan", time.Minute)
	if err := runtime.Start(context.Background()); !errors.Is(err, jobsAbstract.ErrUnknownType) {
		t.Fatalf("err = %v", err)
	}
}

// Every instance schedules; each slot must still run exactly once.
func TestAsynqSchedulerRunsEachSlotOnceAcrossInstances(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	const interval = 2 * time.Second
	recorder := newSlotRecorder()
	register := func(runtime infrastructureJobs.Runtime) {
		runtime.Register("fauscrew:test:tick", recorder.handler)
		runtime.Every("fauscrew:test:tick", interval, jobsAbstract.MaxRetry(0))
	}
	newRuntime(t, redisURL, true, register)
	newRuntime(t, redisURL, true, register)
	newRuntime(t, redisURL, false, register) // an api-only instance

	waitFor(t, "three slots", func() bool { return recorder.slots() >= 3 })
	runs, order := recorder.snapshot()
	for _, slot := range order {
		if runs[slot] != 1 {
			t.Fatalf("slot %s ran %d times", slot, runs[slot])
		}
	}

	slot := order[len(order)-1]
	inspector := newInspector(t, redisURL)
	info, err := inspector.GetTaskInfo(string(jobsAbstract.QueueDefault), infrastructureJobs.PeriodicTaskID("fauscrew:test:tick", slot))
	if err != nil {
		t.Fatalf("slot %s task: %v", slot, err)
	}
	if info.Retention < interval {
		t.Fatalf("slot retention %s, want at least %s", info.Retention, interval)
	}
}
