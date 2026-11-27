package adaptors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"houseflowApi/internal/bootstrap/adaptors"
	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
)

// statsRuntime serves a scripted series of samples; the rest of the runtime
// is unused by the observer.
type statsRuntime struct {
	infrastructureJobs.Runtime
	mutex   sync.Mutex
	samples []statsSample
	served  chan struct{}
}

type statsSample struct {
	stats infrastructureJobs.Stats
	err   error
}

func (r *statsRuntime) Stats(context.Context) (infrastructureJobs.Stats, error) {
	r.mutex.Lock()
	sample := r.samples[0]
	r.samples = r.samples[1:]
	r.mutex.Unlock()
	defer func() { r.served <- struct{}{} }()
	return sample.stats, sample.err
}

type syncBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buffer.Write(p)
}

func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	b.mutex.Lock()
	defer b.mutex.Unlock()
	var result []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.buffer.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("not JSON: %s", line)
		}
		result = append(result, entry)
	}
	return result
}

func TestObserveJobsLogsMetricsAlertsAndOutagesOnce(t *testing.T) {
	queues := func(archived int) infrastructureJobs.Stats {
		return infrastructureJobs.Stats{Backend: "asynq", Queues: []infrastructureJobs.QueueStats{
			{Queue: "fauscrew-default", Archived: archived},
		}}
	}
	outage := errors.New("dial tcp 10.0.0.7:6379: connection refused")
	runtime := &statsRuntime{served: make(chan struct{}), samples: []statsSample{
		{stats: queues(1)},
		{stats: queues(2)},
		{err: outage},
		{err: outage},
		{stats: queues(2)},
	}}
	output := &syncBuffer{}
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		adaptors.ObserveJobs(ctx, runtime, "machine-1", output, ticks)
		close(stopped)
	}()
	for range 5 {
		ticks <- time.Now()
		<-runtime.served
	}
	cancel()
	<-stopped

	var messages []string
	for _, entry := range output.lines(t) {
		message := entry["msg"].(string)
		if kind, ok := entry["kind"]; ok {
			message += "/" + kind.(string)
		}
		if entry["instance"] != "machine-1" {
			t.Fatalf("instance missing: %v", entry)
		}
		messages = append(messages, message)
	}
	want := []string{"jobMetrics", "jobMetrics", "jobAlert/archivedGrew", "jobAlert/statsUnavailable", "jobMetrics"}
	if strings.Join(messages, ",") != strings.Join(want, ",") {
		t.Fatalf("log = %v, want %v", messages, want)
	}
	if strings.Contains(output.buffer.String(), "10.0.0.7") {
		t.Fatal("the raw Valkey error reached the log")
	}
}
