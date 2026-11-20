package jobs

import (
	"bufio"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
)

// Stats is a sample of the job system for telemetry. Queues are cluster-wide
// (read from Valkey); Jobs counts what this instance ran since it started.
type Stats struct {
	Backend string               `json:"backend"`
	Queues  []QueueStats         `json:"queues,omitempty"`
	Jobs    map[string]JobCounts `json:"jobs,omitempty"`
	Valkey  *ValkeyMemory        `json:"valkey,omitempty"`
	// Inline is set by the in-process runtime, which has no queues.
	Inline *InlineStats `json:"inline,omitempty"`
}

type QueueStats struct {
	Queue     string `json:"queue"`
	Pending   int    `json:"pending"`
	Active    int    `json:"active"`
	Scheduled int    `json:"scheduled"`
	Retry     int    `json:"retry"`
	Archived  int    `json:"archived"`
	Completed int    `json:"completed"`
	// LatencyMs is how long the oldest pending job has been waiting.
	LatencyMs      int64 `json:"latencyMs"`
	ProcessedToday int   `json:"processedToday"`
	FailedToday    int   `json:"failedToday"`
	Paused         bool  `json:"paused,omitempty"`
}

type JobCounts struct {
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	AvgMs     int64 `json:"avgMs"`
	MaxMs     int64 `json:"maxMs"`
}

type ValkeyMemory struct {
	UsedBytes int64 `json:"usedBytes"`
	MaxBytes  int64 `json:"maxBytes"`
}

type InlineStats struct {
	Delayed int `json:"delayed"`
	Running int `json:"running"`
}

// jobCounters counts handler runs per job type. Job types come from the
// registry, so the map stays small.
type jobCounters struct {
	mutex  sync.Mutex
	byType map[string]*typeCounter
}

type typeCounter struct {
	succeeded, failed int64
	total, max        time.Duration
}

func newJobCounters() *jobCounters {
	return &jobCounters{byType: make(map[string]*typeCounter)}
}

func (c *jobCounters) observe(jobType string, elapsed time.Duration, err error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	counter, ok := c.byType[jobType]
	if !ok {
		counter = &typeCounter{}
		c.byType[jobType] = counter
	}
	if err == nil {
		counter.succeeded++
	} else {
		counter.failed++
	}
	counter.total += elapsed
	counter.max = max(counter.max, elapsed)
}

func (c *jobCounters) snapshot() map[string]JobCounts {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if len(c.byType) == 0 {
		return nil
	}
	result := make(map[string]JobCounts, len(c.byType))
	for jobType, counter := range c.byType {
		runs := counter.succeeded + counter.failed
		result[jobType] = JobCounts{
			Succeeded: counter.succeeded,
			Failed:    counter.failed,
			AvgMs:     (counter.total / time.Duration(max(runs, 1))).Milliseconds(),
			MaxMs:     counter.max.Milliseconds(),
		}
	}
	return result
}

// parseValkeyMemory reads used_memory and maxmemory from INFO memory.
func parseValkeyMemory(info string) *ValkeyMemory {
	var memory ValkeyMemory
	found := false
	scanner := bufio.NewScanner(strings.NewReader(info))
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), ":")
		if !ok {
			continue
		}
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "used_memory":
			memory.UsedBytes, found = number, true
		case "maxmemory":
			memory.MaxBytes = number
		}
	}
	if !found {
		return nil
	}
	return &memory
}

// Alert thresholds. A queue waits longer the less urgent its jobs are.
const (
	ValkeyMemoryAlertRatio = 0.8
)

var queueLatencyAlert = map[string]time.Duration{
	string(jobsAbstract.QueueCritical): time.Minute,
	string(jobsAbstract.QueueDefault):  5 * time.Minute,
	string(jobsAbstract.QueueLow):      30 * time.Minute,
}

type AlertKind string

const (
	// AlertArchived: jobs ran out of retries (or failed permanently) since
	// the previous sample; see the runbook to inspect and rerun them.
	AlertArchived AlertKind = "archivedGrew"
	// AlertLatency: the oldest pending job waited past its queue's limit.
	AlertLatency AlertKind = "queueLatency"
	// AlertPaused: a queue is paused and its jobs are not processed.
	AlertPaused AlertKind = "queuePaused"
	// AlertValkeyMemory: Valkey is near maxmemory; with noeviction every
	// enqueue fails once it is full.
	AlertValkeyMemory AlertKind = "valkeyMemory"
)

type Alert struct {
	Kind  AlertKind `json:"kind"`
	Queue string    `json:"queue,omitempty"`
	Value int64     `json:"value"`
	Limit int64     `json:"limit"`
}

// Alerts compares a sample with the previous one (nil at startup, which
// reports no archive growth: archived jobs stay for 90 days).
func Alerts(previous *Stats, current Stats) []Alert {
	var alerts []Alert
	previousArchived := make(map[string]int)
	if previous != nil {
		for _, queue := range previous.Queues {
			previousArchived[queue.Queue] = queue.Archived
		}
	}
	for _, queue := range current.Queues {
		if before, ok := previousArchived[queue.Queue]; ok && queue.Archived > before {
			alerts = append(alerts, Alert{Kind: AlertArchived, Queue: queue.Queue, Value: int64(queue.Archived), Limit: int64(before)})
		}
		if limit, ok := queueLatencyAlert[queue.Queue]; ok && queue.LatencyMs > limit.Milliseconds() {
			alerts = append(alerts, Alert{Kind: AlertLatency, Queue: queue.Queue, Value: queue.LatencyMs, Limit: limit.Milliseconds()})
		}
		if queue.Paused {
			alerts = append(alerts, Alert{Kind: AlertPaused, Queue: queue.Queue, Value: 1, Limit: 0})
		}
	}
	if valkey := current.Valkey; valkey != nil && valkey.MaxBytes > 0 {
		limit := int64(float64(valkey.MaxBytes) * ValkeyMemoryAlertRatio)
		if valkey.UsedBytes >= limit {
			alerts = append(alerts, Alert{Kind: AlertValkeyMemory, Value: valkey.UsedBytes, Limit: limit})
		}
	}
	return alerts
}

func knownQueueNames() []string {
	names := make([]string, 0, len(jobsAbstract.Queues))
	for _, queue := range jobsAbstract.Queues {
		names = append(names, string(queue))
	}
	slices.Sort(names)
	return names
}
