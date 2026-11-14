package houseRockets

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/tests/testkit/gamekit"
)

func runtimeFrame(t *testing.T, runtime *houseRockets.Runtime) houseRockets.RuntimeFrame {
	t.Helper()
	select {
	case frame := <-runtime.Frames():
		return frame
	default:
		t.Fatal("missing runtime frame")
		return houseRockets.RuntimeFrame{}
	}
}

func TestHouseRocketsRuntimeCoalescesInputAndFencesControllers(t *testing.T) {
	runtime, now := gamekit.NewRocketsRuntime(t)
	grant := gamekit.BindRocketsControl(t, runtime, "first", "connection-a", now)
	first := gamekit.SteerRockets(grant, "connection-a", 1, 0.2, now)
	if err := runtime.Submit(first, now); err != nil {
		t.Fatal(err)
	}
	second := gamekit.SteerRockets(grant, "connection-a", 2, 0.7, now)
	if err := runtime.Submit(second, now); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(first, now); !errors.Is(err, houseRockets.ErrStaleInput) {
		t.Fatalf("duplicate = %v", err)
	}
	wrong := gamekit.SteerRockets(grant, "connection-a", 900, 0, now)
	wrong.ControlGeneration = "old-generation"
	if err := runtime.Submit(wrong, now); !errors.Is(err, houseRockets.ErrControlRequired) {
		t.Fatalf("wrong generation = %v", err)
	}
	wrong = gamekit.SteerRockets(grant, "connection-a", 900, 0, now)
	wrong.RuntimeEpoch--
	if err := runtime.Submit(wrong, now); !errors.Is(err, houseRockets.ErrStaleInput) {
		t.Fatalf("wrong epoch = %v", err)
	}
	wrong = gamekit.SteerRockets(grant, "connection-a", 3, math.NaN(), now)
	if err := runtime.Submit(wrong, now); !errors.Is(err, houseRockets.ErrInvalidInput) {
		t.Fatalf("NaN = %v", err)
	}
	if err := runtime.Advance(now.Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	frame := runtimeFrame(t, runtime)
	if frame.World.Tick != 6 || frame.Controls[0].LastProcessedInputSequence != 2 || math.Abs(frame.World.Players[0].CourseHeading-0.7) > 1e-12 {
		t.Fatalf("coalesced frame = %+v", frame)
	}
	newGrant := gamekit.BindRocketsControl(t, runtime, "first", "connection-b", now.Add(50*time.Millisecond))
	if newGrant.ControlGeneration == grant.ControlGeneration {
		t.Fatal("takeover must rotate generation")
	}
	if err := runtime.Submit(gamekit.SteerRockets(grant, "connection-a", 3, 0, now.Add(50*time.Millisecond)), now.Add(50*time.Millisecond)); !errors.Is(err, houseRockets.ErrControlRequired) {
		t.Fatalf("old connection = %v", err)
	}
	if err := runtime.Submit(gamekit.SteerRockets(newGrant, "connection-b", 1, -0.1, now.Add(50*time.Millisecond)), now.Add(50*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	old := gamekit.SteerRockets(newGrant, "connection-b", 2, 0, now.Add(-2*time.Second))
	if err := runtime.Submit(old, now.Add(50*time.Millisecond)); !errors.Is(err, houseRockets.ErrInputExpired) {
		t.Fatalf("expired = %v", err)
	}
}

func TestHouseRocketsRuntimeSchedulingAndSlowConsumer(t *testing.T) {
	for _, fps := range []int{30, 60, 120, 144} {
		runtime, now := gamekit.NewRocketsRuntime(t)
		for step := 1; step <= fps; step++ {
			if err := runtime.Advance(now.Add(time.Duration(int64(time.Second) * int64(step) / int64(fps)))); err != nil {
				t.Fatal(err)
			}
		}
		frame := runtimeFrame(t, runtime)
		if frame.World.Tick != 120 || frame.StateSequence != 21 {
			t.Fatalf("fps %d: ticks=%d frames=%d", fps, frame.World.Tick, frame.StateSequence)
		}
	}
	runtime, now := gamekit.NewRocketsRuntime(t)
	if err := runtime.Advance(now.Add(251 * time.Millisecond)); !errors.Is(err, houseRockets.ErrInvalidAdvance) {
		t.Fatalf("overload = %v", err)
	}
	frame := runtimeFrame(t, runtime)
	if frame.Phase != houseRockets.PhaseFinalizing || frame.World.Outcome.EndReason != houseRockets.EndRuntimeOverloaded {
		t.Fatalf("overload frame = %+v", frame)
	}
}

func TestHouseRocketsRuntimeDisconnectGraceAndReconnect(t *testing.T) {
	runtime, now := gamekit.NewRocketsRuntime(t)
	first := gamekit.BindRocketsControl(t, runtime, "first", "a", now)
	second := gamekit.BindRocketsControl(t, runtime, "second", "b", now)
	for _, pair := range []struct {
		grant      houseRockets.HouseRocketsControlGrantedModel
		connection string
	}{{first, "a"}, {second, "b"}} {
		if err := runtime.Submit(gamekit.SteerRockets(pair.grant, pair.connection, 1, math.Pi/2, now), now); err != nil {
			t.Fatal(err)
		}
	}
	for step := 1; step <= 160; step++ {
		current := now.Add(time.Duration(step) * 100 * time.Millisecond)
		if err := runtime.Submit(houseRockets.RuntimeInput{Kind: houseRockets.InputHeartbeat, PlayerID: "second", ConnectionID: "b", RuntimeEpoch: 7, ControlGeneration: second.ControlGeneration, CreatedAt: current}, current); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Advance(current); err != nil {
			t.Fatal(err)
		}
		if step == 60 {
			frame := runtimeFrame(t, runtime)
			if frame.Controls[0].Connected || !frame.World.Players[0].IsAlive {
				t.Fatal("timeout must disconnect without immediate elimination")
			}
		}
	}
	frame := runtimeFrame(t, runtime)
	if frame.World.Players[0].IsAlive || frame.World.Outcome == nil || frame.World.Outcome.WinnerID == nil || *frame.World.Outcome.WinnerID != "second" {
		t.Fatalf("grace result = %+v", frame)
	}
	if err := runtime.Submit(houseRockets.RuntimeInput{Kind: houseRockets.InputBind, PlayerID: "first", ConnectionID: "new", RuntimeEpoch: 7, CreatedAt: now.Add(16 * time.Second)}, now.Add(16*time.Second)); err == nil {
		t.Fatal("eliminated player must not regain control")
	}
	// A live reconnect before expiry gets a new generation and sequence space.
	runtime, now = gamekit.NewRocketsRuntime(t)
	first = gamekit.BindRocketsControl(t, runtime, "first", "a", now)
	if err := runtime.Submit(houseRockets.RuntimeInput{Kind: houseRockets.InputDisconnect, PlayerID: "first", ConnectionID: "a", RuntimeEpoch: 7, ControlGeneration: first.ControlGeneration, CreatedAt: now}, now); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Advance(now); err != nil {
		t.Fatal(err)
	}
	firstNew := gamekit.BindRocketsControl(t, runtime, "first", "new", now.Add(100*time.Millisecond))
	if firstNew.ControlGeneration == first.ControlGeneration {
		t.Fatal("reconnect reused old control generation")
	}
}

func TestHouseRocketsRuntimeBoundsMailboxesAndInputRate(t *testing.T) {
	runtime, now := gamekit.NewRocketsRuntime(t)
	grant := gamekit.BindRocketsControl(t, runtime, "first", "a", now)
	for sequence := int64(1); sequence <= houseRockets.MaximumSteerMessagesPerSecond; sequence++ {
		if err := runtime.Submit(gamekit.SteerRockets(grant, "a", sequence, 0, now), now); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.Submit(gamekit.SteerRockets(grant, "a", 31, 0, now), now); !errors.Is(err, houseRockets.ErrInputRate) {
		t.Fatalf("rate = %v", err)
	}
	for range houseRockets.RuntimeQueueSize {
		if err := runtime.Submit(houseRockets.RuntimeInput{Kind: houseRockets.InputBind, PlayerID: "second", ConnectionID: "b", RuntimeEpoch: 7, CreatedAt: now}, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.Submit(houseRockets.RuntimeInput{Kind: houseRockets.InputBind, PlayerID: "second", ConnectionID: "b", RuntimeEpoch: 7, CreatedAt: now}, now); !errors.Is(err, houseRockets.ErrRuntimeBusy) {
		t.Fatalf("mailbox = %v", err)
	}
}

// Any socket may request a snapshot; a flood is refused as busy and leaves the
// mailbox room for simulation events instead of cancelling the match.
func TestHouseRocketsRuntimeRefusesSnapshotFloodWithoutCancelling(t *testing.T) {
	runtime, now := gamekit.NewRocketsRuntime(t)
	request := houseRockets.RuntimeInput{Kind: houseRockets.InputSnapshot, PlayerID: "spectator", ConnectionID: "s", RuntimeEpoch: 7, CreatedAt: now}
	accepted := 0
	for range houseRockets.RuntimeQueueSize {
		err := runtime.Submit(request, now)
		if err == nil {
			accepted++
		} else if !errors.Is(err, houseRockets.ErrRuntimeBusy) {
			t.Fatal(err)
		}
	}
	if accepted != houseRockets.RuntimeQueueSize/2 {
		t.Fatalf("accepted snapshot requests = %d", accepted)
	}
	if err := runtime.Submit(houseRockets.RuntimeInput{Kind: houseRockets.InputBind, PlayerID: "first", ConnectionID: "a", RuntimeEpoch: 7, CreatedAt: now}, now); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Advance(now); err != nil {
		t.Fatal(err)
	}
	granted := false
	for len(runtime.Events()) > 0 {
		event := <-runtime.Events()
		granted = granted || event.Grant != nil
	}
	if !granted {
		t.Fatal("bind was not granted after the flood")
	}
	if err := runtime.Submit(request, now); err != nil {
		t.Fatal(err)
	}
	if event := <-runtime.Events(); event.Frame == nil || event.Frame.Phase != houseRockets.PhasePlaying {
		t.Fatalf("match after flood: %+v", event)
	}
}

func TestHouseRocketsRuntimeStopsOnLeaseLossAndConcurrentInput(t *testing.T) {
	runtime, now := gamekit.NewRocketsRuntime(t)
	grant := gamekit.BindRocketsControl(t, runtime, "first", "a", now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); runtime.Run(ctx, func() bool { return false }) }()
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for sequence := int64(1); sequence < 100; sequence++ {
				current := time.Now()
				_ = runtime.Submit(gamekit.SteerRockets(grant, "a", sequence, 0, current), current)
			}
		}()
	}
	workers.Wait()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop on lease loss")
	}
	if err := runtime.Submit(gamekit.SteerRockets(grant, "a", 101, 0, time.Now()), time.Now()); !errors.Is(err, houseRockets.ErrSimulationFinished) {
		t.Fatalf("stopped input = %v", err)
	}
}
