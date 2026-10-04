package tests

import (
	"errors"
	"sync"
	"testing"
	"time"

	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	"houseflowApi/internal/config"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/infrastructure/telemetry"
)

func TestRealtimeLimitsRequireExplicitRolloutCapacity(t *testing.T) {
	t.Setenv("APP_ENV", "staging")
	t.Setenv("HOUSE_ROCKETS_ENABLED", "true")
	t.Setenv("REALTIME_MAX_OWNED_ROOMS", "")
	t.Setenv("REALTIME_MAX_CONNECTIONS", "")
	if _, err := config.LoadRealtimeLimits(); err == nil {
		t.Fatal("enabled rollout has no limits")
	}
	t.Setenv("REALTIME_MAX_OWNED_ROOMS", "4")
	t.Setenv("REALTIME_MAX_CONNECTIONS", "32")
	limits, err := config.LoadRealtimeLimits()
	if err != nil || limits.MaxOwnedRooms != 4 || limits.MaxConnections != 32 {
		t.Fatalf("limits: %+v %v", limits, err)
	}
	for _, invalid := range []string{"0", "-1", "abc"} {
		t.Setenv("REALTIME_MAX_CONNECTIONS", invalid)
		if _, err := config.LoadRealtimeLimits(); err == nil {
			t.Fatal("invalid capacity accepted")
		}
	}
	t.Setenv("REALTIME_MAX_CONNECTIONS", "")
	t.Setenv("REALTIME_MAX_OWNED_ROOMS", "")
	t.Setenv("APP_ENV", "production")
	if !config.HouseRocketsEnabled() {
		t.Fatal("explicit flag ignored in production")
	}
	if _, err := config.LoadRealtimeLimits(); err == nil {
		t.Fatal("enabled production rollout has no limits")
	}
	t.Setenv("HOUSE_ROCKETS_ENABLED", "false")
	if _, err := config.LoadRealtimeLimits(); err != nil {
		t.Fatal(err)
	}
}

func TestDurationHistogramIsConcurrentBoundedAndReportsUpperBuckets(t *testing.T) {
	var histogram telemetry.DurationHistogram
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 1000 {
				histogram.Observe(7 * time.Millisecond)
			}
		}()
	}
	workers.Wait()
	result := histogram.Snapshot()
	if result.Count != 8000 || result.MeanMilliseconds != 7 || result.MaxMilliseconds != 7 || result.P95UpperMilliseconds != 10 {
		t.Fatalf("histogram: %+v", result)
	}
}

func TestRocketsMeasurementsObserveAppliedCoalescedInputAndBoundedOverload(t *testing.T) {
	runtime, now := newRocketsRuntime(t)
	metrics := &realtime.Metrics{}
	runtime.SetObserver(metrics)
	grant := bindRocketsControl(t, runtime, "first", "measurementConnection", now)
	for sequence := int64(1); sequence <= 3; sequence++ {
		if err := runtime.Submit(steerRockets(grant, "measurementConnection", sequence, 0, now), now); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.Advance(now.Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	snapshot := metrics.Snapshot()
	if snapshot.InputsCoalesced != 2 || snapshot.ServerInputAge.Count != 1 || snapshot.ServerInputAge.MaxMilliseconds != 20 || snapshot.PhysicsStep.Count < 1 {
		t.Fatalf("missing measurements: %+v", snapshot)
	}
	if err := runtime.Advance(now.Add(time.Second)); !errors.Is(err, houseRockets.ErrInvalidAdvance) {
		t.Fatalf("unbounded catchup accepted: %v", err)
	}
	if metrics.Snapshot().RuntimeOverloads != 1 {
		t.Fatal("overload unobserved")
	}
}
