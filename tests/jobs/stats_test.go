package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
	"houseflowApi/tests/testkit"
)

func TestAlertsFireOnArchiveGrowthLatencyPauseAndMemory(t *testing.T) {
	previous := infrastructureJobs.Stats{Queues: []infrastructureJobs.QueueStats{
		{Queue: "fauscrew-default", Archived: 2},
		{Queue: "fauscrew-critical", Archived: 0},
	}}
	current := infrastructureJobs.Stats{
		Queues: []infrastructureJobs.QueueStats{
			{Queue: "fauscrew-default", Archived: 3, LatencyMs: (5*time.Minute + time.Second).Milliseconds()},
			{Queue: "fauscrew-critical", Archived: 0, LatencyMs: (59 * time.Second).Milliseconds()},
			{Queue: "fauscrew-low", Archived: 9, LatencyMs: (29 * time.Minute).Milliseconds(), Paused: true},
		},
		Valkey: &infrastructureJobs.ValkeyMemory{UsedBytes: 81, MaxBytes: 100},
	}
	got := map[string]infrastructureJobs.Alert{}
	for _, alert := range infrastructureJobs.Alerts(&previous, current) {
		got[string(alert.Kind)+"/"+alert.Queue] = alert
	}
	want := []string{"archivedGrew/fauscrew-default", "queueLatency/fauscrew-default", "queuePaused/fauscrew-low", "valkeyMemory/"}
	if len(got) != len(want) {
		t.Fatalf("alerts = %+v", got)
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing %s in %+v", key, got)
		}
	}
	if alert := got["archivedGrew/fauscrew-default"]; alert.Value != 3 || alert.Limit != 2 {
		t.Fatalf("archive alert %+v", alert)
	}
	// The low queue's archive is new to this sample, not growth.
	if _, ok := got["archivedGrew/fauscrew-low"]; ok {
		t.Fatal("a queue missing from the previous sample reported growth")
	}
}

func TestAlertsAtStartupIgnoreOldArchiveAndUnlimitedMemory(t *testing.T) {
	current := infrastructureJobs.Stats{
		Queues: []infrastructureJobs.QueueStats{{Queue: "fauscrew-default", Archived: 40}},
		Valkey: &infrastructureJobs.ValkeyMemory{UsedBytes: 1 << 30, MaxBytes: 0},
	}
	if alerts := infrastructureJobs.Alerts(nil, current); len(alerts) != 0 {
		t.Fatalf("alerts = %+v", alerts)
	}
}

func TestInlineStatsCountRunsPerType(t *testing.T) {
	runtime := infrastructureJobs.NewInlineRuntime()
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	runtime.Register("fauscrew:test:ok", func(context.Context, []byte) error { return nil })
	runtime.Register("fauscrew:test:fail", func(context.Context, []byte) error { return errors.New("boom") })
	runtime.Register("fauscrew:test:panic", func(context.Context, []byte) error { panic("boom") })
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, jobType := range []string{"fauscrew:test:ok", "fauscrew:test:ok", "fauscrew:test:fail", "fauscrew:test:panic"} {
		if err := runtime.Enqueue(context.Background(), jobsAbstract.Job{Type: jobType}); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.Enqueue(context.Background(), jobsAbstract.Job{Type: "fauscrew:test:ok"}, jobsAbstract.ProcessIn(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var stats infrastructureJobs.Stats
	waitFor(t, "four runs", func() bool {
		stats, _ = runtime.Stats(context.Background())
		total := int64(0)
		for _, counts := range stats.Jobs {
			total += counts.Succeeded + counts.Failed
		}
		return total == 4 && stats.Inline.Running == 0
	})
	if stats.Backend != "inline" || stats.Inline.Delayed != 1 || len(stats.Queues) != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if ok := stats.Jobs["fauscrew:test:ok"]; ok.Succeeded != 2 || ok.Failed != 0 {
		t.Fatalf("ok = %+v", ok)
	}
	if fail := stats.Jobs["fauscrew:test:fail"]; fail.Failed != 1 {
		t.Fatalf("fail = %+v", fail)
	}
	if panicked := stats.Jobs["fauscrew:test:panic"]; panicked.Failed != 1 {
		t.Fatalf("panic = %+v", panicked)
	}
}

func TestAsynqStatsReadQueuesArchiveAndValkeyMemory(t *testing.T) {
	redisURL := testkit.PrepareRedis(t)
	done := make(chan struct{}, 4)
	runtime := newRuntime(t, redisURL, true, func(runtime infrastructureJobs.Runtime) {
		runtime.Register("fauscrew:test:ok", func(context.Context, []byte) error {
			done <- struct{}{}
			return nil
		})
		runtime.Register("fauscrew:test:dead", func(context.Context, []byte) error {
			done <- struct{}{}
			return jobsAbstract.ErrPermanent
		})
	})
	ctx := context.Background()
	if err := runtime.Enqueue(ctx, jobsAbstract.Job{Type: "fauscrew:test:ok"}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(ctx, jobsAbstract.Job{Type: "fauscrew:test:dead"}, jobsAbstract.InQueue(jobsAbstract.QueueCritical)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Enqueue(ctx, jobsAbstract.Job{Type: "fauscrew:test:ok"}, jobsAbstract.InQueue(jobsAbstract.QueueLow), jobsAbstract.ProcessIn(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-done:
		case <-time.After(waitTimeout):
			t.Fatal("jobs did not run")
		}
	}

	var stats infrastructureJobs.Stats
	waitFor(t, "the dead job in the archive", func() bool {
		var err error
		stats, err = runtime.Stats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, queue := range stats.Queues {
			if queue.Queue == "fauscrew-critical" && queue.Archived == 1 {
				return true
			}
		}
		return false
	})
	queues := map[string]infrastructureJobs.QueueStats{}
	for _, queue := range stats.Queues {
		queues[queue.Queue] = queue
	}
	if stats.Backend != "asynq" || len(queues) != 3 || queues["fauscrew-low"].Scheduled != 1 ||
		queues["fauscrew-default"].ProcessedToday != 1 || queues["fauscrew-critical"].FailedToday != 1 {
		t.Fatalf("queues = %+v", stats.Queues)
	}
	if stats.Valkey == nil || stats.Valkey.UsedBytes <= 0 {
		t.Fatalf("valkey = %+v", stats.Valkey)
	}
	if ok, dead := stats.Jobs["fauscrew:test:ok"], stats.Jobs["fauscrew:test:dead"]; ok.Succeeded != 1 || dead.Failed != 1 {
		t.Fatalf("jobs = %+v", stats.Jobs)
	}
}
