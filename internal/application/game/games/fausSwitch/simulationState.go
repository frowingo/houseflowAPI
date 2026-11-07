package fausSwitch

import (
	"errors"
	"math"
	"slices"
)

const (
	simulationStateVersion = 1
	// solidTolerance is how deep a restored robot may sit in a solid: contact
	// resolution leaves robots within a few 1e-12 of a surface.
	solidTolerance = 1e-6
)

var errInvalidSimulationState = errors.New("faus switch: invalid simulation state")

// simulationState is the simulation part of a runtime checkpoint: the live
// world and where the race stands. The rewind ring and input logs are not
// kept. A restored race cannot rewind before the tick it was restored at,
// and the live world already holds that tick's inputs.
type simulationState struct {
	SchemaVersion int           `json:"schemaVersion"`
	CourseVersion int           `json:"courseVersion"`
	Tick          int64         `json:"tick"`
	StoppedAtTick *int64        `json:"stoppedAtTick"`
	Outcome       *outcomeState `json:"outcome"`
	Runners       []runnerState `json:"runners"`
}

type outcomeState struct {
	Status    ResultStatus `json:"status"`
	EndReason EndReason    `json:"endReason"`
	Tick      int64        `json:"tick"`
}

// runnerState is a robot as the checkpoint and the snapshot carry it. Floats
// are exact: encoding/json writes the shortest text that reads back as the
// same float64.
type runnerState struct {
	X                 float64            `json:"x"`
	Y                 float64            `json:"y"`
	VelocityY         float64            `json:"velocityY"`
	Gravity           Gravity            `json:"gravity"`
	BoostedUntilTick  int64              `json:"boostedUntilTick"`
	UsedBoosts        []int              `json:"usedBoosts"`
	SlowedUntilTick   int64              `json:"slowedUntilTick"`
	UsedSlows         []int              `json:"usedSlows"`
	Status            RunnerStatus       `json:"status"`
	FinishedAtTick    *int64             `json:"finishedAtTick"`
	EliminatedAtTick  *int64             `json:"eliminatedAtTick"`
	EliminationReason *EliminationReason `json:"eliminationReason"`
}

func runnerStateOf(robot *runner) runnerState {
	state := runnerState{X: robot.x, Y: robot.y, VelocityY: robot.velocityY, Gravity: robot.gravity,
		BoostedUntilTick: robot.boostedUntilTick, UsedBoosts: robot.usedBoosts.ids(),
		SlowedUntilTick: robot.slowedUntilTick, UsedSlows: robot.usedSlows.ids(), Status: robot.status}
	switch robot.status {
	case RunnerFinished:
		tick := robot.finishedAtTick
		state.FinishedAtTick = &tick
	case RunnerCrashed:
		tick, reason := robot.crashedAtTick, robot.crashReason
		state.EliminatedAtTick, state.EliminationReason = &tick, &reason
	}
	return state
}

func (race *simulation) state() simulationState {
	state := simulationState{SchemaVersion: simulationStateVersion, CourseVersion: CourseVersion, Tick: race.tick,
		Runners: make([]runnerState, race.count)}
	if race.stoppedAt != noTick {
		stopped := race.stoppedAt
		state.StoppedAtTick = &stopped
	}
	if race.outcome != nil {
		state.Outcome = &outcomeState{Status: race.outcome.status, EndReason: race.outcome.endReason, Tick: race.outcome.tick}
	}
	for seat := range race.count {
		state.Runners[seat] = runnerStateOf(&race.world[seat])
	}
	return state
}

// restoreSimulation resumes a race from a checkpoint. It accepts only a state
// the simulation could have reached: robots consistent with their status,
// clear of the course and inside the camera, and an end that follows from it.
func restoreSimulation(state simulationState) (*simulation, error) {
	count := len(state.Runners)
	if state.SchemaVersion != simulationStateVersion || state.CourseVersion != CourseVersion ||
		count < MinimumPlayers || count > MaximumPlayers || state.Tick < 0 || state.Tick > MaximumMatchTicks {
		return nil, errInvalidSimulationState
	}
	rules := newRacePhysics(count)
	var live world
	for seat, saved := range state.Runners {
		robot, ok := restoreRunner(&rules, saved, state.Tick)
		if !ok {
			return nil, errInvalidSimulationState
		}
		live[seat] = robot
	}
	race := resumeSimulation(rules, count, state.Tick, live)
	stopped := noTick
	if state.StoppedAtTick != nil {
		stopped = *state.StoppedAtTick
	}
	running := anyRunning(race.runners())
	// stoppedAt is set exactly while nobody runs, and it is never later than
	// the tick (resumeSimulation sets it to the tick for a stopped race).
	if running != (stopped == noTick) || (stopped != noTick && (stopped < 0 || stopped > state.Tick)) {
		return nil, errInvalidSimulationState
	}
	race.stoppedAt = stopped
	if !validOutcome(state.Outcome, state.Tick, stopped) {
		return nil, errInvalidSimulationState
	}
	if state.Outcome != nil {
		race.outcome = &raceOutcome{status: state.Outcome.Status, endReason: state.Outcome.EndReason, tick: state.Outcome.Tick}
	}
	return race, nil
}

func validOutcome(outcome *outcomeState, tick, stopped int64) bool {
	windowEnd := min(stopped+raceEndWindowTicks, MaximumMatchTicks)
	if outcome == nil {
		return tick < MaximumMatchTicks && (stopped == noTick || tick < windowEnd)
	}
	switch {
	case outcome.Status == ResultCompleted && outcome.EndReason == EndRaceComplete:
		return stopped != noTick && outcome.Tick == stopped && tick == windowEnd
	case outcome.Status == ResultCompleted && outcome.EndReason == EndTimeLimit:
		return stopped == noTick && outcome.Tick == MaximumMatchTicks && tick == MaximumMatchTicks
	case outcome.Status == ResultCancelled:
		return outcome.EndReason.Cancels() && outcome.Tick == tick
	}
	return false
}

func restoreRunner(rules *physics, saved runnerState, tick int64) (runner, bool) {
	finite := func(values ...float64) bool {
		for _, value := range values {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return false
			}
		}
		return true
	}
	robot := runner{x: saved.X, y: saved.Y, velocityY: saved.VelocityY, gravity: saved.Gravity, status: saved.Status,
		boostedUntilTick: saved.BoostedUntilTick, slowedUntilTick: saved.SlowedUntilTick}
	boosts, okBoosts := restorePads(saved.UsedBoosts, len(rules.course.boosts))
	slows, okSlows := restorePads(saved.UsedSlows, len(rules.course.slows))
	robot.usedBoosts, robot.usedSlows = boosts, slows
	// A boost or slow window opens only on a pad, and at most a pad's length
	// after the tick.
	timer := func(until int64, length int64, pads []int) bool {
		return until >= 0 && until <= tick+length && (until == 0 || len(pads) > 0)
	}
	if !finite(saved.X, saved.Y, saved.VelocityY) || (saved.Gravity != GravityDown && saved.Gravity != GravityUp) || !okBoosts || !okSlows ||
		!timer(saved.BoostedUntilTick, boostTicks, saved.UsedBoosts) || !timer(saved.SlowedUntilTick, slowTicks, saved.UsedSlows) {
		return runner{}, false
	}
	body := rules.size.body(&robot)
	switch saved.Status {
	case RunnerRunning:
		// A running robot is on the playfield, inside the camera, short of
		// the finish and clear of every solid.
		if saved.FinishedAtTick != nil || saved.EliminatedAtTick != nil || saved.EliminationReason != nil ||
			robot.y < -boundaryMargin || robot.y > worldHeight+boundaryMargin ||
			!(body.maxX > rules.course.cameraTrail(tick)) || !(body.maxX < rules.course.finishX-finishReach) {
			return runner{}, false
		}
		inside := false
		rules.course.visitSolidsNear(body, func(wall *solid) { inside = inside || body.depth(wall.box) >= solidTolerance })
		if inside {
			return runner{}, false
		}
	case RunnerFinished:
		if saved.FinishedAtTick == nil || *saved.FinishedAtTick < 1 || *saved.FinishedAtTick > tick ||
			saved.EliminatedAtTick != nil || saved.EliminationReason != nil || saved.VelocityY != 0 {
			return runner{}, false
		}
		robot.finishedAtTick = *saved.FinishedAtTick
	case RunnerCrashed:
		if saved.EliminatedAtTick == nil || *saved.EliminatedAtTick < 0 || *saved.EliminatedAtTick > tick ||
			saved.EliminationReason == nil || saved.FinishedAtTick != nil || saved.VelocityY != 0 {
			return runner{}, false
		}
		switch *saved.EliminationReason {
		case EliminationBoundary, EliminationLeftBehind, EliminationForfeit, EliminationMembershipRevoked:
		default:
			return runner{}, false
		}
		robot.crashedAtTick, robot.crashReason = *saved.EliminatedAtTick, *saved.EliminationReason
	default:
		return runner{}, false
	}
	return robot, true
}

// restorePads reads a strictly increasing list of pad ids below count.
func restorePads(ids []int, count int) (padSet, bool) {
	var set padSet
	if !slices.IsSorted(ids) {
		return 0, false
	}
	for index, id := range ids {
		if id < 0 || id >= count || (index > 0 && ids[index-1] == id) {
			return 0, false
		}
		set.add(id)
	}
	return set, true
}
