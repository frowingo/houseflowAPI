package fausSwitch

import (
	"cmp"
	"encoding/json"
	"math/rand/v2"
	"slices"
	"testing"
)

// raceOf starts a race at tick 0 from the given robots under rules.
func raceOf(rules physics, robots ...runner) *simulation {
	var live world
	copy(live[:], robots)
	return resumeSimulation(rules, len(robots), 0, live)
}

// runTo advances the race to tick.
func (race *simulation) runTo(t *testing.T, tick int64) {
	t.Helper()
	for race.tick < tick && race.outcome == nil {
		if err := race.advance(int(min(tick-race.tick, maximumAdvanceTicks))); err != nil {
			t.Fatal(err)
		}
	}
}

func (race *simulation) flipAt(t *testing.T, seat int, sequence, clientTick int64) flipRequest {
	t.Helper()
	requests := []flipRequest{{seat: seat, sequence: sequence, clientTick: clientTick}}
	if err := race.applyFlips(requests); err != nil {
		t.Fatal(err)
	}
	return requests[0]
}

// The briefing's rewind test: a flip that arrives 250 ms late ends the same
// as one on time, also for the lower of two stacked robots and for robots
// that meet sole to sole.
func TestLateFlipEqualsOnTimeFlip(t *testing.T) {
	stackedBottom := spawnAlone(GravityDown, 420)
	stackedTop := stackedBottom
	stackedTop.y += 90
	soleFloor, soleCeiling := spawnAlone(GravityDown, 420), spawnAlone(GravityUp, 420)
	rules := solo()
	rules.flipAlone(&soleFloor)
	rules.flipAlone(&soleCeiling)
	scenarios := []struct {
		name  string
		start func() *simulation
		seat  int
		tick  int64
	}{
		{"start grid", func() *simulation { race, _ := newSimulation(2); return race }, 0, 150},
		{"lower of two stacked", func() *simulation { return raceOf(solo(), stackedTop, stackedBottom) }, 1, 60},
		{"upper of two stacked", func() *simulation { return raceOf(solo(), stackedTop, stackedBottom) }, 0, 60},
		{"sole to sole", func() *simulation { return raceOf(solo(), soleFloor, soleCeiling) }, 1, 90},
	}
	for _, scenario := range scenarios {
		onTime, late := scenario.start(), scenario.start()
		onTime.runTo(t, scenario.tick)
		first := onTime.flipAt(t, scenario.seat, 1, scenario.tick)
		late.runTo(t, scenario.tick+MaximumRewindTicks)
		second := late.flipAt(t, scenario.seat, 1, scenario.tick)
		if first.outcome != flipped || second.outcome != flipped || second.tick != scenario.tick {
			t.Fatalf("%s: outcomes %v %v at %d", scenario.name, first.outcome, second.outcome, second.tick)
		}
		onTime.runTo(t, scenario.tick+120)
		late.runTo(t, scenario.tick+120)
		if onTime.world != late.world {
			t.Fatalf("%s: the late flip ended elsewhere", scenario.name)
		}
		// And both match the bare physics with the flip at that tick.
		direct := scenario.start()
		runners := direct.runners()
		for tick := int64(0); tick < scenario.tick+120; tick++ {
			if tick == scenario.tick {
				direct.rules.flip(runners, scenario.seat)
			}
			direct.rules.step(runners, tick)
		}
		if direct.world != onTime.world {
			t.Fatalf("%s: the simulation differs from the physics", scenario.name)
		}
	}
}

// Random flips from every seat, up to 40 ticks late, in batches of one
// scheduling step, plus forfeits, always end where the same inputs applied on
// time at their (clamped) ticks end: the physics run once from the start.
func TestRewindsMatchOnTimeInputs(t *testing.T) {
	type input struct {
		tick    int64
		flip    flipRecord
		forfeit bool
	}
	for count := MinimumPlayers; count <= MaximumPlayers; count++ {
		for seed := range uint64(6) {
			random := rand.New(rand.NewPCG(uint64(count)*31, seed))
			race, _ := newSimulation(count)
			var inputs []input
			sequences := make([]int64, count)
			for race.outcome == nil {
				var batch []flipRequest
				for seat := range count {
					if random.IntN(12) == 0 {
						sequences[seat]++
						batch = append(batch, flipRequest{seat: seat, sequence: sequences[seat], clientTick: race.tick - int64(random.IntN(41))})
					}
				}
				random.Shuffle(len(batch), func(i, j int) { batch[i], batch[j] = batch[j], batch[i] })
				if err := race.applyFlips(batch); err != nil {
					t.Fatal(err)
				}
				for _, request := range batch {
					if request.tick < race.tick-MaximumRewindTicks || request.tick > race.tick {
						t.Fatalf("flip clamped to %d at %d", request.tick, race.tick)
					}
					if request.outcome != flipNotRunning {
						inputs = append(inputs, input{tick: request.tick, flip: flipRecord{seat: request.seat, sequence: request.sequence}})
					}
				}
				if random.IntN(400) == 0 {
					seat := random.IntN(count)
					if err := race.eliminate(seat, EliminationForfeit); err != nil {
						t.Fatal(err)
					}
					inputs = append(inputs, input{tick: race.tick, flip: flipRecord{seat: seat}, forfeit: true})
				}
				if err := race.advance(2); err != nil {
					t.Fatal(err)
				}
			}
			// Replay on time: forfeits first, then flips by seat and sequence.
			slices.SortStableFunc(inputs, func(a, b input) int {
				if a.forfeit != b.forfeit && a.tick == b.tick {
					if a.forfeit {
						return -1
					}
					return 1
				}
				return cmp.Or(cmp.Compare(a.tick, b.tick), compareFlips(a.flip, b.flip))
			})
			reference, _ := newSimulation(count)
			runners := reference.runners()
			stopped := noTick
			for tick := int64(0); ; tick++ {
				for len(inputs) > 0 && inputs[0].tick == tick {
					if inputs[0].forfeit {
						crash(&runners[inputs[0].flip.seat], EliminationForfeit, tick)
					} else {
						reference.rules.flip(runners, inputs[0].flip.seat)
					}
					inputs = inputs[1:]
				}
				if stopped == noTick && !anyRunning(runners) {
					stopped = tick
				}
				if tick == race.tick {
					break
				}
				reference.rules.step(runners, tick)
			}
			if reference.world != race.world || stopped != race.stoppedAt {
				t.Fatalf("%d robots, seed %d: rewound race differs from the on-time replay (stopped %d, %d)", count, seed, stopped, race.stoppedAt)
			}
		}
	}
}

// One batch of flips from several seats rewinds once and ends where the same
// flips applied one by one end.
func TestCoalescedRewindEqualsOneByOne(t *testing.T) {
	batched, single := newRace(t, 8), newRace(t, 8)
	batched.runTo(t, 600)
	single.runTo(t, 600)
	batch := []flipRequest{{seat: 5, sequence: 1, clientTick: 580}, {seat: 0, sequence: 1, clientTick: 575},
		{seat: 3, sequence: 1, clientTick: 598}, {seat: 0, sequence: 2, clientTick: 590}}
	if err := batched.applyFlips(slices.Clone(batch)); err != nil {
		t.Fatal(err)
	}
	for _, request := range batch {
		single.flipAt(t, request.seat, request.sequence, request.clientTick)
	}
	if batched.world != single.world {
		t.Fatal("batched rewind differs")
	}
	batched.runTo(t, 900)
	single.runTo(t, 900)
	if batched.world != single.world {
		t.Fatal("batched rewind differs later")
	}
}

func newRace(t *testing.T, count int) *simulation {
	t.Helper()
	race, err := newSimulation(count)
	if err != nil {
		t.Fatal(err)
	}
	return race
}

func TestFlipTicksAreClamped(t *testing.T) {
	race := newRace(t, 2)
	race.runTo(t, 5)
	if request := race.flipAt(t, 0, 1, -3); request.tick != 0 {
		t.Fatalf("before the start: %d", request.tick)
	}
	race.runTo(t, 100)
	if request := race.flipAt(t, 0, 2, 10); request.tick != 100-MaximumRewindTicks {
		t.Fatalf("too old: %d", request.tick)
	}
	if request := race.flipAt(t, 1, 1, 500); request.tick != 100 {
		t.Fatalf("future: %d", request.tick)
	}
	data, _ := json.Marshal(race.state())
	var state simulationState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	restored, err := restoreSimulation(state)
	if err != nil {
		t.Fatal(err)
	}
	restored.runTo(t, 110)
	if request := restored.flipAt(t, 0, 3, 85); request.tick != 100 {
		t.Fatalf("before the restore: %d", request.tick)
	}
	if err := restored.applyFlips([]flipRequest{{seat: 2}}); err != errInvalidRace {
		t.Fatalf("seat 2 of 2: %v", err)
	}
}

func TestUnsupportedFlipIsANoOpAndRefusedFlipsAreDropped(t *testing.T) {
	flipped, untouched := newRace(t, 2), newRace(t, 2)
	flipped.runTo(t, 150)
	untouched.runTo(t, 150)
	if request := flipped.flipAt(t, 0, 1, 150); request.outcome != flipped.rules.flip(slices.Clone(untouched.runners()), 0) {
		t.Fatal("outcome")
	}
	// Mid-air: the second flip right after the first finds no surface.
	if request := flipped.flipAt(t, 0, 2, 150); request.outcome != flipUnsupported {
		t.Fatalf("mid-air flip: %v", request.outcome)
	}
	if len(flipped.flips[150&rewindMask]) != 2 {
		t.Fatal("an unsupported flip stays in the log")
	}
	// A robot that forfeited at 160 refuses flips from 160 on, and the
	// refused flip is not replayed; a flip from before 160 still counts and
	// the forfeit survives the rewind.
	flipped.runTo(t, 160)
	if err := flipped.eliminate(1, EliminationForfeit); err != nil {
		t.Fatal(err)
	}
	flipped.runTo(t, 170)
	if request := flipped.flipAt(t, 1, 1, 165); request.outcome != flipNotRunning || len(flipped.flips[165&rewindMask]) != 0 {
		t.Fatalf("flip after the forfeit: %v", request.outcome)
	}
	if request := flipped.flipAt(t, 1, 2, 155); request.outcome == flipNotRunning {
		t.Fatal("a flip from before the forfeit counts")
	}
	robot := flipped.world[1]
	if robot.status != RunnerCrashed || robot.crashReason != EliminationForfeit || robot.crashedAtTick != 160 {
		t.Fatalf("the forfeit was lost in the rewind: %+v", robot)
	}
}

// A flip and a forfeit of the same robot in the same tick end the same way
// live and after a rewind: the forfeit comes first.
func TestEliminationsComeBeforeFlipsOfTheirTick(t *testing.T) {
	race := newRace(t, 2)
	race.runTo(t, 150)
	race.flipAt(t, 0, 1, 150)
	if err := race.eliminate(0, EliminationForfeit); err != nil {
		t.Fatal(err)
	}
	live := race.world
	race.runTo(t, 160)
	race.flipAt(t, 1, 1, 145)
	if race.world[0] != live[0] || race.world[0].gravity != GravityDown {
		t.Fatalf("robot 0 live %+v, after the rewind %+v", live[0], race.world[0])
	}
}

// K2: a race completes 30 ticks after the last robot stopped, at that tick;
// inside the window a late flip can bring the last robot back.
func TestRaceEndWindow(t *testing.T) {
	race := newRace(t, 2)
	race.runTo(t, 10)
	race.eliminate(0, EliminationForfeit)
	race.eliminate(1, EliminationMembershipRevoked)
	race.runTo(t, 10+raceEndWindowTicks-1)
	if race.outcome != nil || race.stoppedAt != 10 {
		t.Fatalf("outcome %+v stopped %d", race.outcome, race.stoppedAt)
	}
	race.runTo(t, 10+raceEndWindowTicks)
	if race.outcome == nil || *race.outcome != (raceOutcome{status: ResultCompleted, endReason: EndRaceComplete, tick: 10}) {
		t.Fatalf("outcome %+v", race.outcome)
	}
	if err := race.applyFlips([]flipRequest{{seat: 0, sequence: 1, clientTick: 20}}); err != errRaceOver {
		t.Fatal("a finished race takes no flips")
	}

	// The last robot is pinned to a wall and left behind at 517; a flip from
	// tick 492 that arrives at 522 takes it over the wall, until 731.
	wall := firstFloorWall(t)
	other := spawnAlone(GravityUp, wall.minX-200)
	rescued := raceOf(solo(), spawnAlone(GravityDown, wall.minX-20), other)
	rescued.eliminate(1, EliminationForfeit)
	rescued.runTo(t, 522)
	if rescued.stoppedAt != 517 || rescued.world[0].crashReason != EliminationLeftBehind {
		t.Fatalf("stopped %d robot %+v", rescued.stoppedAt, rescued.world[0])
	}
	if request := rescued.flipAt(t, 0, 1, 492); request.outcome != flipped {
		t.Fatalf("rescue flip: %v", request.outcome)
	}
	if rescued.stoppedAt != noTick || !rescued.world[0].running() {
		t.Fatal("the late flip brings the robot back")
	}
	rescued.runTo(t, 731+raceEndWindowTicks-1)
	if rescued.outcome != nil || rescued.stoppedAt != 731 {
		t.Fatalf("outcome %+v stopped %d", rescued.outcome, rescued.stoppedAt)
	}
	rescued.runTo(t, 731+raceEndWindowTicks)
	if rescued.outcome == nil || rescued.outcome.tick != 731 || rescued.outcome.endReason != EndRaceComplete {
		t.Fatalf("outcome %+v", rescued.outcome)
	}
}

func TestTimeLimitIsFinal(t *testing.T) {
	near := func(robots ...runner) *simulation {
		var live world
		copy(live[:], robots)
		return resumeSimulation(newRacePhysics(len(robots)), len(robots), MaximumMatchTicks-10, live)
	}
	gone := runner{status: RunnerCrashed, crashReason: EliminationForfeit, crashedAtTick: 5, gravity: GravityDown}
	race := near(spawnAlone(GravityDown, 19_700), gone)
	race.runTo(t, MaximumMatchTicks)
	if race.outcome == nil || *race.outcome != (raceOutcome{status: ResultCompleted, endReason: EndTimeLimit, tick: MaximumMatchTicks}) ||
		!race.world[0].running() {
		t.Fatalf("outcome %+v robot %+v", race.outcome, race.world[0])
	}
	// Stopped inside the last window: complete at the stop, without waiting.
	stopped := near(spawnAlone(GravityDown, 19_700), gone)
	stopped.runTo(t, MaximumMatchTicks-5)
	stopped.eliminate(0, EliminationForfeit)
	stopped.runTo(t, MaximumMatchTicks)
	if stopped.outcome == nil || *stopped.outcome != (raceOutcome{status: ResultCompleted, endReason: EndRaceComplete, tick: MaximumMatchTicks - 5}) {
		t.Fatalf("outcome %+v", stopped.outcome)
	}
}

func TestCancel(t *testing.T) {
	race := newRace(t, 3)
	race.runTo(t, 40)
	if race.cancel(EndRaceComplete) != errInvalidRace {
		t.Fatal("raceComplete is not a cancellation")
	}
	if race.cancel(EndInsufficientPlayers) != nil || race.cancel(EndInsufficientPlayers) != nil || race.cancel(EndRecoveryFailed) != errRaceOver {
		t.Fatal("cancel is idempotent for one reason")
	}
	if race.advance(1) != errRaceOver || race.eliminate(0, EliminationForfeit) != errRaceOver {
		t.Fatal("a cancelled race is over")
	}
	result, err := race.result([]string{"a", "b", "c"})
	if err != nil || result.status != ResultCancelled || result.tick != 40 || result.winnerID != nil {
		t.Fatalf("result %+v %v", result, err)
	}
	for _, player := range result.players {
		if player.Rank != nil {
			t.Fatal("a cancelled race has no ranks")
		}
	}
}

func TestResultRanksTheRobots(t *testing.T) {
	grid := startRunners(3)
	race := raceOf(newRacePhysics(3), grid[0], grid[1], runner{status: RunnerFinished, finishedAtTick: 9, x: 20_280, gravity: GravityDown})
	if _, err := race.result([]string{"a", "b", "c"}); err != errRaceNotOver {
		t.Fatal("no result before the end")
	}
	race.runTo(t, 10)
	race.eliminate(0, EliminationForfeit)
	race.runTo(t, 20)
	race.eliminate(1, EliminationMembershipRevoked)
	race.runTo(t, 20+raceEndWindowTicks)
	result, err := race.result([]string{"a", "b", "c"})
	if err != nil || result.status != ResultCompleted || result.endReason != EndRaceComplete || result.tick != 20 ||
		result.winnerID == nil || *result.winnerID != "c" {
		t.Fatalf("result %+v %v", result, err)
	}
	want := []int{3, 2, 1}
	for seat, player := range result.players {
		if player.Rank == nil || *player.Rank != want[seat] || player.Distance != race.world[seat].x {
			t.Fatalf("seat %d: %+v", seat, player)
		}
	}
	if _, err := race.result([]string{"a"}); err != errInvalidRace {
		t.Fatal("one id per seat")
	}
}

// A checkpoint round trip through JSON continues exactly like the original.
func TestCheckpointRoundTrip(t *testing.T) {
	random := rand.New(rand.NewPCG(8, 8))
	original := newRace(t, 8)
	sequences := make([]int64, 8)
	flips := func(race *simulation, floor int64, rolls []int) {
		var batch []flipRequest
		for seat, roll := range rolls {
			if roll < 8 {
				batch = append(batch, flipRequest{seat: seat, sequence: sequences[seat] + 1, clientTick: max(floor, race.tick-int64(roll*3))})
			}
		}
		if err := race.applyFlips(batch); err != nil {
			t.Fatal(err)
		}
	}
	roll := func() []int {
		rolls := make([]int, 8)
		for seat := range rolls {
			rolls[seat] = random.IntN(60)
		}
		return rolls
	}
	for original.tick < 900 {
		flips(original, 0, roll())
		for seat := range sequences {
			sequences[seat]++
		}
		original.advance(3)
	}
	original.eliminate(4, EliminationForfeit)
	data, err := json.Marshal(original.state())
	if err != nil {
		t.Fatal(err)
	}
	var state simulationState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	restored, err := restoreSimulation(state)
	if err != nil {
		t.Fatal(err)
	}
	if restored.world != original.world || restored.stoppedAt != original.stoppedAt {
		t.Fatal("restored world differs")
	}
	floor := original.tick
	for original.outcome == nil {
		rolls := roll()
		flips(original, floor, rolls)
		flips(restored, floor, rolls)
		for seat := range sequences {
			sequences[seat]++
		}
		original.advance(3)
		restored.advance(3)
		if original.world != restored.world {
			t.Fatalf("restored race diverged at tick %d", original.tick)
		}
	}
	if restored.outcome == nil || *restored.outcome != *original.outcome {
		t.Fatal("outcomes differ")
	}
	// A finished race restores too.
	data, _ = json.Marshal(original.state())
	json.Unmarshal(data, &state)
	if again, err := restoreSimulation(state); err != nil || *again.outcome != *original.outcome {
		t.Fatalf("finished race: %v", err)
	}
}

func TestRestoreRejectsImpossibleStates(t *testing.T) {
	race := newRace(t, 3)
	race.runTo(t, 300)
	race.eliminate(2, EliminationForfeit)
	valid := race.state()
	if _, err := restoreSimulation(valid); err != nil {
		t.Fatalf("valid state: %v", err)
	}
	ptr := func(value int64) *int64 { return &value }
	mutations := map[string]func(state *simulationState){
		"schema":                 func(state *simulationState) { state.SchemaVersion = 2 },
		"course":                 func(state *simulationState) { state.CourseVersion = 6 },
		"one robot":              func(state *simulationState) { state.Runners = state.Runners[:1] },
		"negative tick":          func(state *simulationState) { state.Tick = -1 },
		"past the limit":         func(state *simulationState) { state.Tick = MaximumMatchTicks + 1 },
		"stopped while one runs": func(state *simulationState) { state.StoppedAtTick = ptr(200) },
		"inside a wall":          func(state *simulationState) { state.Runners[0].Y = floorSurface },
		"behind the camera":      func(state *simulationState) { state.Runners[0].X = 100 },
		"off the playfield":      func(state *simulationState) { state.Runners[0].Y = -200 },
		"bad gravity":            func(state *simulationState) { state.Runners[0].Gravity = 0 },
		"unknown pad":            func(state *simulationState) { state.Runners[0].UsedBoosts = []int{15} },
		"unsorted pads":          func(state *simulationState) { state.Runners[0].UsedSlows = []int{1, 0} },
		"boost without pad": func(state *simulationState) {
			state.Runners[0].BoostedUntilTick = 310
			state.Runners[0].UsedBoosts = nil
		},
		"running and finished": func(state *simulationState) { state.Runners[0].FinishedAtTick = ptr(10) },
		"crash in the future":  func(state *simulationState) { state.Runners[2].EliminatedAtTick = ptr(301) },
		"crash without reason": func(state *simulationState) { state.Runners[2].EliminationReason = nil },
		"moving crash":         func(state *simulationState) { state.Runners[2].VelocityY = 1 },
		"finished at 0": func(state *simulationState) {
			state.Runners[2].Status, state.Runners[2].FinishedAtTick = RunnerFinished, ptr(0)
			state.Runners[2].EliminatedAtTick, state.Runners[2].EliminationReason = nil, nil
		},
		"timeLimit early": func(state *simulationState) {
			state.Outcome = &outcomeState{Status: ResultCompleted, EndReason: EndTimeLimit, Tick: MaximumMatchTicks}
		},
		"raceComplete while running": func(state *simulationState) {
			state.Outcome = &outcomeState{Status: ResultCompleted, EndReason: EndRaceComplete, Tick: 300}
		},
		"cancelled as completed": func(state *simulationState) {
			state.Outcome = &outcomeState{Status: ResultCancelled, EndReason: EndRaceComplete, Tick: 300}
		},
		"crash reason none": func(state *simulationState) {
			none := EliminationReason(0)
			state.Runners[2].EliminationReason = &none
		},
	}
	for name, mutate := range mutations {
		state := race.state()
		mutate(&state)
		if _, err := restoreSimulation(state); err != errInvalidSimulationState {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// After warm-up a rewind allocates nothing: the ring, logs and batch are reused.
func TestRewindDoesNotAllocate(t *testing.T) {
	race := newRace(t, MaximumPlayers)
	race.runTo(t, 300)
	batch := make([]flipRequest, MaximumPlayers)
	sequence := int64(0)
	rewind := func() {
		sequence++
		for seat := range batch {
			batch[seat] = flipRequest{seat: seat, sequence: sequence, clientTick: race.tick - MaximumRewindTicks + int64(seat)}
		}
		race.applyFlips(batch)
		race.advance(1)
	}
	for range 2 * rewindSlots {
		rewind()
	}
	if allocations := testing.AllocsPerRun(100, rewind); allocations != 0 {
		t.Fatalf("%v allocations per rewind", allocations)
	}
}

// BenchmarkRewind8 is the briefing's eight-player rewind: one flip 30 ticks
// late replays the whole world for 30 ticks.
func BenchmarkRewind8(b *testing.B) {
	race, _ := newSimulation(MaximumPlayers)
	for race.tick < 300 {
		race.advance(maximumAdvanceTicks)
	}
	batch := make([]flipRequest, 1)
	sequence := int64(0)
	b.ReportAllocs()
	for b.Loop() {
		sequence++
		batch[0] = flipRequest{seat: int(sequence % MaximumPlayers), sequence: sequence, clientTick: race.tick - MaximumRewindTicks}
		race.applyFlips(batch)
		// Keep the race at the same place so every rewind costs the same.
		race.flips[(race.tick-MaximumRewindTicks)&rewindMask] = race.flips[(race.tick-MaximumRewindTicks)&rewindMask][:0]
	}
}
