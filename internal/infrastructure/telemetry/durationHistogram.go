package telemetry

import (
	"sync/atomic"
	"time"
)

var durationBounds = [...]time.Duration{time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, time.Second}

// Fixed buckets keep memory bounded regardless of traffic/room/user count.
type DurationHistogram struct {
	count   atomic.Uint64
	sum     atomic.Uint64
	maximum atomic.Uint64
	buckets [len(durationBounds) + 1]atomic.Uint64
}

type DurationSummary struct {
	Count                uint64  `json:"count"`
	MeanMilliseconds     float64 `json:"meanMilliseconds"`
	MaxMilliseconds      float64 `json:"maxMilliseconds"`
	P95UpperMilliseconds float64 `json:"p95UpperMilliseconds"`
}

func (histogram *DurationHistogram) Observe(duration time.Duration) {
	if duration < 0 {
		duration = 0
	}
	value := uint64(duration)
	histogram.count.Add(1)
	histogram.sum.Add(value)
	for old := histogram.maximum.Load(); value > old; old = histogram.maximum.Load() {
		if histogram.maximum.CompareAndSwap(old, value) {
			break
		}
	}
	index := len(durationBounds)
	for candidate, bound := range durationBounds {
		if duration <= bound {
			index = candidate
			break
		}
	}
	histogram.buckets[index].Add(1)
}

// Percentiles are bucket upper bounds, not exact quantiles. Snapshots taken
// during writes are approximate; no unbounded latency sample list is retained.
func (histogram *DurationHistogram) Snapshot() DurationSummary {
	count := histogram.count.Load()
	maximum := histogram.maximum.Load()
	result := DurationSummary{Count: count, MaxMilliseconds: float64(maximum) / float64(time.Millisecond)}
	if count == 0 {
		return result
	}
	result.MeanMilliseconds = float64(histogram.sum.Load()) / float64(count) / float64(time.Millisecond)
	target := count - count/20
	var cumulative uint64
	result.P95UpperMilliseconds = result.MaxMilliseconds
	for index, bound := range durationBounds {
		cumulative += histogram.buckets[index].Load()
		if cumulative >= target {
			result.P95UpperMilliseconds = float64(bound) / float64(time.Millisecond)
			break
		}
	}
	return result
}
