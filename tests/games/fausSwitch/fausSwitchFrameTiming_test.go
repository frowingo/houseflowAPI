package fausSwitch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"houseflowApi/internal/application/game/games/fausSwitch"
	"houseflowApi/internal/application/game/module"
)

// switchDriver steps a runtime on a virtual clock, one scheduler step at a
// time, as the room's scheduler does.
type switchDriver struct {
	t       *testing.T
	runtime module.TickRuntime
	now     time.Time
}

const switchStep = time.Second / fausSwitch.SchedulingRateHz

func (driver *switchDriver) submit(input fausSwitch.RuntimeInput) {
	driver.t.Helper()
	input.RuntimeEpoch, input.CreatedAt = 1, driver.now
	payload, err := json.Marshal(input)
	if err != nil {
		driver.t.Fatal(err)
	}
	if err := driver.runtime.Submit(payload, driver.now); err != nil {
		driver.t.Fatalf("submit %s: %v", input.Kind, err)
	}
}

// step advances one scheduler step and returns what it published.
func (driver *switchDriver) step() (frames []fausSwitch.FausSwitchSnapshotModel, grants []module.ControlGrant) {
	driver.t.Helper()
	driver.now = driver.now.Add(switchStep)
	if err := driver.runtime.Advance(driver.now); err != nil {
		driver.t.Fatal(err)
	}
	return driver.drain()
}

func (driver *switchDriver) drain() (frames []fausSwitch.FausSwitchSnapshotModel, grants []module.ControlGrant) {
	driver.t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		update, err := driver.runtime.NextUpdate(ctx)
		cancel()
		if err != nil {
			return frames, grants
		}
		switch {
		case update.Frame != nil:
			var frame fausSwitch.RuntimeFrame
			if err := json.Unmarshal(update.Frame.Payload, &frame); err != nil {
				driver.t.Fatal(err)
			}
			frames = append(frames, fausSwitch.SnapshotForConnection(frame, "", "", nil))
		case update.Grant != nil:
			grants = append(grants, *update.Grant)
		case update.Err != nil:
			driver.t.Fatalf("runtime rejected an input: %v", update.Err)
		}
	}
}

// A flip that turns a robot is published in the scheduler step that applies
// it, ahead of the periodic snapshot; a flip that cannot turn the robot
// (mid-air) waits for the periodic snapshot, whose schedule the extra frame
// does not move.
func TestFausSwitchAppliedFlipIsPublishedImmediately(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Millisecond)
	runtime, err := fausSwitch.Module().NewRuntime(module.RuntimeParams{SessionID: "timing-session", HouseID: "timing-house", StartedAt: start,
		Players: []module.Player{{PlayerID: "player-0", DisplayName: "Zero"}, {PlayerID: "player-1", DisplayName: "One"}}, Epoch: 1, Now: start})
	if err != nil {
		t.Fatal(err)
	}
	driver := &switchDriver{t: t, runtime: runtime, now: start}
	driver.drain()

	driver.submit(fausSwitch.RuntimeInput{MessageID: "bind", Kind: fausSwitch.InputBind, PlayerID: "player-0", ConnectionID: "connection-0"})
	_, grants := driver.step()
	if len(grants) != 1 {
		t.Fatalf("grants after bind = %+v", grants)
	}
	generation := grants[0].ControlGeneration

	// Wait for a periodic frame that shows the robot running on a rail. A
	// periodic snapshot is three scheduler steps apart, so a frame in the
	// next step can only come from the flip.
	var periodic fausSwitch.FausSwitchSnapshotModel
	for found := false; !found; {
		if driver.now.Sub(start) > 3*time.Second {
			t.Fatal("robot never stood on a rail at a periodic frame")
		}
		frames, _ := driver.step()
		if len(frames) == 1 && standing(frames[0].Runners[0]) {
			periodic, found = frames[0], true
		}
	}

	driver.submit(fausSwitch.RuntimeInput{MessageID: "flip-1", Kind: fausSwitch.InputFlip, PlayerID: "player-0", ConnectionID: "connection-0",
		ControlGeneration: generation, InputSequence: 1, ClientTick: periodic.Tick})
	frames, _ := driver.step()
	if len(frames) != 1 {
		t.Fatalf("frames in the step that applied the flip = %d, want 1", len(frames))
	}
	flippedFrame := frames[0]
	runner := flippedFrame.Runners[0]
	if runner.Gravity == periodic.Runners[0].Gravity || runner.LastProcessedInputSequence != 1 || runner.VelocityY == 0 {
		t.Fatalf("immediate frame does not show the flip: before %+v after %+v", periodic.Runners[0], runner)
	}

	// The robot is in the air: this flip is confirmed but turns nothing.
	driver.submit(fausSwitch.RuntimeInput{MessageID: "flip-2", Kind: fausSwitch.InputFlip, PlayerID: "player-0", ConnectionID: "connection-0",
		ControlGeneration: generation, InputSequence: 2, ClientTick: flippedFrame.Tick})
	if frames, _ := driver.step(); len(frames) != 0 {
		t.Fatalf("a mid-air flip published %d frames before the periodic snapshot", len(frames))
	}
	// The periodic snapshot arrives on its original schedule.
	frames, _ = driver.step()
	if len(frames) == 0 {
		frames, _ = driver.step()
	}
	if len(frames) != 1 {
		t.Fatalf("periodic frame after the flips = %d frames", len(frames))
	}
	if got := frames[0].Runners[0]; got.LastProcessedInputSequence != 2 || got.Gravity != runner.Gravity {
		t.Fatalf("periodic frame after the mid-air flip: %+v", got)
	}
	if frames[0].StateSequence != periodic.StateSequence+2 {
		t.Fatalf("state sequence %d, want %d (periodic, flip, periodic)", frames[0].StateSequence, periodic.StateSequence+2)
	}
}
