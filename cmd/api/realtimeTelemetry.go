package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	"houseflowApi/internal/infrastructure/coordination"
	"houseflowApi/internal/infrastructure/realtime"
)

func observeRealtime(ctx context.Context, coordinator coordinationAbstract.Coordinator, service *realtime.Service) {
	if service == nil {
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-service.Errors():
			logger.Warn("realtimeError", "source", "gateway", "category", realtimeErrorCategory(event.Err))
		case event := <-service.RoomErrors():
			logger.Warn("realtimeError", "source", "room", "category", realtimeErrorCategory(event.Err))
		case <-ticker.C:
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			attributes := []any{"instance", coordinator.InstanceID(), "realtime", service.Metrics(), "goHeapBytes", memory.HeapAlloc, "goRuntimeBytes", memory.Sys, "goroutines", runtime.NumGoroutine()}
			if provider, ok := coordinator.(interface {
				Metrics() coordination.RedisMetricsSnapshot
			}); ok {
				attributes = append(attributes, "redis", provider.Metrics())
			}
			logger.Info("realtimeMetrics", attributes...)
		}
	}
}

// Bounded categories, not raw errors which might include a URL or credentials.
func realtimeErrorCategory(err error) string {
	switch {
	case errors.Is(err, realtime.ErrRealtimeCapacity):
		return "capacity"
	case errors.Is(err, coordinationAbstract.ErrLeaseLost):
		return "leaseLost"
	case errors.Is(err, coordinationAbstract.ErrUnavailable):
		return "coordinationUnavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadlineExceeded"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "commandOrStorage"
	}
}
