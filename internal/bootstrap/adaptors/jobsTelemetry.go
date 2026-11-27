package adaptors

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"

	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
)

const (
	jobTelemetryInterval = time.Minute
	jobStatsTimeout      = 10 * time.Second
)

// ObserveJobs logs a jobMetrics line every minute and a jobAlert warning for
// each alert rule that fires (see infrastructureJobs.Alerts), until ctx ends.
// Queue figures are cluster-wide, so with several instances each logs the
// same queues; the per-type job counts are the instance's own. A nil output
// is stdout and nil ticks a one-minute ticker; tests pass their own.
func ObserveJobs(ctx context.Context, runtime infrastructureJobs.Runtime, instanceID string, output io.Writer, ticks <-chan time.Time) {
	if output == nil {
		output = os.Stdout
	}
	if ticks == nil {
		ticker := time.NewTicker(jobTelemetryInterval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	logger := slog.New(slog.NewJSONHandler(output, nil))
	var previous *infrastructureJobs.Stats
	unavailable := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
		sampleCtx, cancel := context.WithTimeout(ctx, jobStatsTimeout)
		stats, err := runtime.Stats(sampleCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// Reported once per outage; the raw error can carry the address.
			if !unavailable {
				logger.Warn("jobAlert", "instance", instanceID, "kind", "statsUnavailable", "category", jobStatsErrorCategory(err))
			}
			unavailable = true
			continue
		}
		unavailable = false
		logger.Info("jobMetrics", "instance", instanceID, "jobs", stats)
		for _, alert := range infrastructureJobs.Alerts(previous, stats) {
			logger.Warn("jobAlert", "instance", instanceID, "kind", alert.Kind, "queue", alert.Queue, "value", alert.Value, "limit", alert.Limit)
		}
		previous = &stats
	}
}

func jobStatsErrorCategory(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadlineExceeded"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "valkey"
	}
}
