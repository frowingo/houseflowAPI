package fausSwitch

import (
	"cmp"
	"errors"
	"slices"
)

// The simulation is a race in ticks: the world, the flips and eliminations
// that change it, a rewind ring for late flips and the race-end window. It
// knows no clock, connection or player identity; seats are join order. The
// runtime owns it and calls it from one goroutine.
//
// Timing. The world "at tick t" is the state the steps reached at t; the
// inputs of tick t (eliminations first, then flips) apply to it, then the
// step from t to t+1 runs. The live world is the state at the current tick
// with the inputs that arrived so far, so every input takes effect at once.
// ring[t] keeps the state at t before its inputs and the logs keep the inputs
// of each of the last ticks, so a rewind to tick c reloads ring[c] and replays
// inputs and steps up to now.

const (
	// rewindSlots holds the world of now and of the MaximumRewindTicks
	// before it; a power of two, so a tick's slot is tick & rewindMask.
	rewindSlots = 32
	rewindMask  = rewindSlots - 1
	// raceEndWindowTicks: once nobody runs, the race completes this much
	// later (K2, 250 ms), the furthest a late flip can still reach back.
	raceEndWindowTicks = MaximumRewindTicks
	// maximumAdvanceTicks bounds one advance (a quarter second).
	maximumAdvanceTicks = PhysicsRateHz / 4
	noTick              = int64(-1)
)

// The ring must hold now and every tick a flip may reach back to.
var _ [rewindSlots - MaximumRewindTicks - 1]struct{}

var (
	errRaceOver       = errors.New("faus switch: the race is over")
	errInvalidRace    = errors.New("faus switch: invalid race")
	errInvalidAdvance = errors.New("faus switch: an advance is 0 to 30 ticks")
)

// world is every seat's robot; a race uses the first count of them. It is a
// value, so saving or loading one is a single copy.
type world [MaximumPlayers]runner

// flipRequest is one accepted flip input of a batch. applyFlips sets tick (the
// clamped tick it applies at) and outcome; flipNotRunning means the robot had
// already finished or crashed at that tick, so the flip is refused and never
// replayed. An unsupported flip is a no-op but stays in the log: a later
// rewind may give it a surface.
type flipRequest struct {
	seat       int
	sequence   int64
	clientTick int64
	tick       int64
	outcome    flipOutcome
}

// flipRecord is a logged flip; request is its index in the batch being
// applied, or -1 once it is settled.
type flipRecord struct {
	seat     int
	sequence int64
	request  int
}

// Flips of one tick apply by seat, then input sequence. Flips never move a
// body, so flips of different seats commute and arrival order cannot change
// a race.
func compareFlips(a, b flipRecord) int {
	return cmp.Or(cmp.Compare(a.seat, b.seat), cmp.Compare(a.sequence, b.sequence))
}

// elimination is a forfeit or a membership revocation. It is logged like a
// flip, so a rewind over its tick keeps it.
type elimination struct {
	seat   int
	reason EliminationReason
}

// raceOutcome is how and at which tick the race ended. A completed race is
// ranked as the world stands at tick (nobody moves after a raceComplete
// tick, and timeLimit ends at MaximumMatchTicks).
type raceOutcome struct {
	status    ResultStatus
	endReason EndReason
	tick      int64
}

type simulation struct {
	rules physics
	count int
	tick  int64
	world world
	// base is the oldest tick a flip may reach back to: 0, or the tick a
	// checkpoint was restored at.
	base int64
	// stoppedAt is the first tick from which no robot has run, or noTick
	// while one runs (K2).
	stoppedAt int64
	outcome   *raceOutcome
	ring      [rewindSlots]world
	flips     [rewindSlots][]flipRecord
	events    [rewindSlots][]elimination
}

// newSimulation starts a race of count robots on their start grid at tick 0.
func newSimulation(count int) (*simulation, error) {
	if count < MinimumPlayers || count > MaximumPlayers {
		return nil, errInvalidRace
	}
	var start world
	for seat, lane := range startLanes(count, runnerScale(count)) {
		start[seat] = lane.spawn
	}
	return resumeSimulation(newRacePhysics(count), count, 0, start), nil
}

// resumeSimulation continues a race from the live world at tick; no flip may
// reach back before it.
func resumeSimulation(rules physics, count int, tick int64, live world) *simulation {
	race := &simulation{rules: rules, count: count, tick: tick, world: live, base: tick, stoppedAt: noTick}
	race.ring[tick&rewindMask] = live
	race.noteStopped(tick)
	return race
}

func (race *simulation) runners() []runner { return race.world[:race.count] }

// advance steps the race ticks times, stopping once it has an outcome.
func (race *simulation) advance(ticks int) error {
	if ticks < 0 || ticks > maximumAdvanceTicks {
		return errInvalidAdvance
	}
	if ticks > 0 && race.outcome != nil {
		return errRaceOver
	}
	for range ticks {
		race.rules.step(race.runners(), race.tick)
		race.tick++
		slot := race.tick & rewindMask
		race.ring[slot] = race.world
		race.flips[slot] = race.flips[slot][:0]
		race.events[slot] = race.events[slot][:0]
		race.noteStopped(race.tick)
		if race.settle(); race.outcome != nil {
			break
		}
	}
	return nil
}

// settle ends the race once the end window has passed or time is up. A race
// nobody runs in at the time limit completes at the tick it stopped.
func (race *simulation) settle() {
	stopped := race.stoppedAt != noTick
	switch {
	case stopped && (race.tick >= race.stoppedAt+raceEndWindowTicks || race.tick == MaximumMatchTicks):
		race.outcome = &raceOutcome{status: ResultCompleted, endReason: EndRaceComplete, tick: race.stoppedAt}
	case race.tick == MaximumMatchTicks:
		race.outcome = &raceOutcome{status: ResultCompleted, endReason: EndTimeLimit, tick: race.tick}
	}
}

func (race *simulation) noteStopped(tick int64) {
	if race.stoppedAt == noTick && !anyRunning(race.runners()) {
		race.stoppedAt = tick
	}
}

// eliminate takes a robot out of the race now. It is logged even when the
// robot has already stopped: a later rewind may find it running at this tick.
// The current tick is replayed rather than patched, so this tick's flips
// still come after its eliminations, as in every later replay.
func (race *simulation) eliminate(seat int, reason EliminationReason) error {
	if race.outcome != nil {
		return errRaceOver
	}
	if seat < 0 || seat >= race.count || (reason != EliminationForfeit && reason != EliminationMembershipRevoked) {
		return errInvalidRace
	}
	slot := race.tick & rewindMask
	race.events[slot] = append(race.events[slot], elimination{seat: seat, reason: reason})
	race.replay(race.tick, nil)
	return nil
}

// applyFlips applies the flips of one scheduling step with a single rewind
// to the earliest of their ticks. Each flip applies at its client tick,
// clamped to the last MaximumRewindTicks (and never before base) and to now,
// as if it had arrived on time.
func (race *simulation) applyFlips(requests []flipRequest) error {
	if race.outcome != nil {
		return errRaceOver
	}
	for index := range requests {
		if seat := requests[index].seat; seat < 0 || seat >= race.count {
			return errInvalidRace
		}
	}
	if len(requests) == 0 {
		return nil
	}
	oldest := max(race.base, race.tick-MaximumRewindTicks)
	earliest := race.tick
	for index := range requests {
		request := &requests[index]
		request.tick = min(max(request.clientTick, oldest), race.tick)
		earliest = min(earliest, request.tick)
		record := flipRecord{seat: request.seat, sequence: request.sequence, request: index}
		log := &race.flips[request.tick&rewindMask]
		at, _ := slices.BinarySearchFunc(*log, record, compareFlips)
		*log = slices.Insert(*log, at, record)
	}
	race.replay(earliest, requests)
	return nil
}

// replay reloads the world at tick from and runs it forward to now with
// every logged input, rewriting the ring on the way.
func (race *simulation) replay(from int64, requests []flipRequest) {
	if race.stoppedAt >= from {
		race.stoppedAt = noTick
	}
	race.world = race.ring[from&rewindMask]
	for tick := from; ; tick++ {
		if tick > from {
			race.ring[tick&rewindMask] = race.world
		}
		race.applyInputs(tick, requests)
		race.noteStopped(tick)
		if tick == race.tick {
			return
		}
		race.rules.step(race.runners(), tick)
	}
}

// applyInputs applies a tick's eliminations, then its flips, settling the
// flips of the batch being applied.
func (race *simulation) applyInputs(tick int64, requests []flipRequest) {
	slot := tick & rewindMask
	runners := race.runners()
	for _, event := range race.events[slot] {
		crash(&runners[event.seat], event.reason, tick)
	}
	log := race.flips[slot]
	kept := log[:0]
	for _, record := range log {
		outcome := race.rules.flip(runners, record.seat)
		if record.request >= 0 {
			requests[record.request].outcome = outcome
			if outcome == flipNotRunning {
				continue
			}
			record.request = -1
		}
		kept = append(kept, record)
	}
	race.flips[slot] = kept
}

// cancel ends the race without ranks; cancelling again for the same reason
// changes nothing.
func (race *simulation) cancel(reason EndReason) error {
	if !reason.Cancels() {
		return errInvalidRace
	}
	if race.outcome != nil {
		if race.outcome.status == ResultCancelled && race.outcome.endReason == reason {
			return nil
		}
		return errRaceOver
	}
	race.outcome = &raceOutcome{status: ResultCancelled, endReason: reason, tick: race.tick}
	return nil
}

func anyRunning(runners []runner) bool {
	for index := range runners {
		if runners[index].running() {
			return true
		}
	}
	return false
}
