package fausSwitch

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"slices"
	"testing"
)

// fixtures/fausSwitchSimulation.json is the client's own recording of the
// Swift engine (plan §9, item 1), copied unchanged from the client's
// FausCrew/shared/Fixtures. Without the file this test skips;
// TestSwiftParityTrace covers the same ground with traces built here.
//
// Each scenario starts from the start grid of runnerCount robots on its race
// course. Tick by tick, the flips listed for that tick are applied in list
// order, then the field steps. A checkpoint at tick T is the state after the
// step that reaches T, before T's flips.
type simulationFixture struct {
	CourseVersion int                  `json:"courseVersion"`
	Scenarios     []simulationScenario `json:"scenarios"`
}

type simulationScenario struct {
	Name        string           `json:"name"`
	RunnerCount int              `json:"runnerCount"`
	Flips       []simulationFlip `json:"flips"`
	Checkpoints []struct {
		Tick    int64              `json:"tick"`
		Runners []simulationRunner `json:"runners"`
	} `json:"checkpoints"`
}

type simulationFlip struct {
	Tick int64 `json:"tick"`
	Seat int   `json:"seat"`
}

type simulationRunner struct {
	X                 float64            `json:"x"`
	Y                 float64            `json:"y"`
	VelocityY         float64            `json:"velocityY"`
	Gravity           Gravity            `json:"gravity"`
	Status            RunnerStatus       `json:"status"`
	FinishedAtTick    *int64             `json:"finishedAtTick"`
	EliminatedAtTick  *int64             `json:"eliminatedAtTick"`
	EliminationReason *EliminationReason `json:"eliminationReason"`
}

func simulationRunnerOf(robot *runner) simulationRunner {
	state := simulationRunner{X: robot.x, Y: robot.y, VelocityY: robot.velocityY, Gravity: robot.gravity, Status: robot.status}
	switch robot.status {
	case RunnerFinished:
		state.FinishedAtTick = &robot.finishedAtTick
	case RunnerCrashed:
		state.EliminatedAtTick, state.EliminationReason = &robot.crashedAtTick, &robot.crashReason
	}
	return state
}

func (want simulationRunner) matches(got simulationRunner) bool {
	same := func(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }
	equal := func(a, b *int64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	return same(want.X, got.X) && same(want.Y, got.Y) && same(want.VelocityY, got.VelocityY) &&
		want.Gravity == got.Gravity && want.Status == got.Status &&
		equal(want.FinishedAtTick, got.FinishedAtTick) && equal(want.EliminatedAtTick, got.EliminatedAtTick) &&
		(want.EliminationReason == nil) == (got.EliminationReason == nil) &&
		(want.EliminationReason == nil || *want.EliminationReason == *got.EliminationReason)
}

// simulationFixtureSHA256 pins the copy to the client's recording; the client
// pins the same digest in FausSwitchSimulationFixtureTests. A regenerated
// fixture (course or physics change) updates both.
const simulationFixtureSHA256 = "5bf34d8ddd81cdf4773565212e7ee389d8bd985ab55e359e9864a3d6ce807976"

func TestClientSimulationFixture(t *testing.T) {
	data, err := os.ReadFile("fixtures/fausSwitchSimulation.json")
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("fixtures/fausSwitchSimulation.json is not here yet (requested from the client)")
	}
	if err != nil {
		t.Fatal(err)
	}
	if digest := sha256.Sum256(data); hex.EncodeToString(digest[:]) != simulationFixtureSHA256 {
		t.Fatalf("fixture sha256 %x, want %s", digest, simulationFixtureSHA256)
	}
	var fixture simulationFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.CourseVersion != CourseVersion || len(fixture.Scenarios) == 0 {
		t.Fatalf("courseVersion %d with %d scenarios", fixture.CourseVersion, len(fixture.Scenarios))
	}
	reasons := map[EliminationReason]bool{}
	for _, scenario := range fixture.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			if scenario.RunnerCount < 1 || scenario.RunnerCount > MaximumPlayers {
				t.Fatalf("runnerCount %d", scenario.RunnerCount)
			}
			rules := newRacePhysics(scenario.RunnerCount)
			runners := startRunners(scenario.RunnerCount)
			flips, tick := scenario.Flips, int64(0)
			if !slices.IsSortedFunc(flips, func(a, b simulationFlip) int { return cmp.Compare(a.Tick, b.Tick) }) {
				t.Fatal("flips are not in tick order")
			}
			for _, checkpoint := range scenario.Checkpoints {
				for ; tick < checkpoint.Tick; tick++ {
					for len(flips) > 0 && flips[0].Tick == tick {
						rules.flip(runners, flips[0].Seat)
						flips = flips[1:]
					}
					rules.step(runners, tick)
				}
				if tick != checkpoint.Tick || len(checkpoint.Runners) != len(runners) {
					t.Fatalf("checkpoint at tick %d with %d runners is out of order", checkpoint.Tick, len(checkpoint.Runners))
				}
				for seat, want := range checkpoint.Runners {
					if got := simulationRunnerOf(&runners[seat]); !want.matches(got) {
						t.Fatalf("tick %d seat %d: got %+v, want %+v", checkpoint.Tick, seat, got, want)
					}
				}
			}
			// The recording is a whole race: every flip lands before the
			// last checkpoint, where every robot has stopped.
			if len(flips) > 0 {
				t.Fatalf("%d flips after the last checkpoint are not checked", len(flips))
			}
			stopped := map[RunnerStatus]int{}
			for seat := range runners {
				if runners[seat].running() {
					t.Fatalf("seat %d still running at the last checkpoint (tick %d)", seat, tick)
				}
				stopped[runners[seat].status]++
				if runners[seat].status == RunnerCrashed {
					reasons[runners[seat].crashReason] = true
				}
			}
			t.Logf("%d robots, %d flips, %d checkpoints to tick %d: %v", scenario.RunnerCount, len(scenario.Flips), len(scenario.Checkpoints), tick, stopped)
		})
	}
	if !reasons[EliminationBoundary] || !reasons[EliminationLeftBehind] {
		t.Fatalf("fixture eliminates by %v; want both boundary and leftBehind", reasons)
	}
}
