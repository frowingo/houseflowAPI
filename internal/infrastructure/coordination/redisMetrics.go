package coordination

import (
	"context"
	"net"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"houseflowApi/internal/infrastructure/telemetry"
)

type redisMeasurements struct {
	commands atomic.Uint64
	failures atomic.Uint64
	duration telemetry.DurationHistogram
}

type RedisMetricsSnapshot struct {
	ClientCommands uint64                    `json:"clientCommands"`
	ClientFailures uint64                    `json:"clientFailures"`
	ClientDuration telemetry.DurationSummary `json:"clientDuration"`
}

func (coordinator *RedisCoordinator) Metrics() RedisMetricsSnapshot {
	return RedisMetricsSnapshot{ClientCommands: coordinator.measurements.commands.Load(), ClientFailures: coordinator.measurements.failures.Load(), ClientDuration: coordinator.measurements.duration.Snapshot()}
}

func (metrics *redisMeasurements) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (metrics *redisMeasurements) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, command redis.Cmder) error {
		start := time.Now()
		err := next(ctx, command)
		metrics.commands.Add(1)
		metrics.duration.Observe(time.Since(start))
		if err != nil && err != redis.Nil {
			metrics.failures.Add(1)
		}
		return err
	}
}
func (metrics *redisMeasurements) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, commands []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, commands)
		metrics.commands.Add(uint64(len(commands)))
		metrics.duration.Observe(time.Since(start))
		for _, command := range commands {
			if command.Err() != nil && command.Err() != redis.Nil {
				metrics.failures.Add(1)
			}
		}
		return err
	}
}
