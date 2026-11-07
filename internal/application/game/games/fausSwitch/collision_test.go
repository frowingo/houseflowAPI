package fausSwitch

import (
	"math"
	"math/rand/v2"
	"testing"
)

// FausSwitchCollisionTests: robots are solid to each other, stacked, sole to
// sole or nose to tail, never inside.

// assertNoRobotOverlap fails when two running robots overlap by more than the
// contact tolerance on both axes.
func assertNoRobotOverlap(t *testing.T, rules *physics, runners []runner, tick int64) {
	t.Helper()
	for i := range runners {
		for j := i + 1; j < len(runners); j++ {
			if !runners[i].running() || !runners[j].running() {
				continue
			}
			if depth := overlapDepth(rules.size.body(&runners[i]), rules.size.body(&runners[j])); depth >= 1e-6 {
				t.Fatalf("robots %d and %d overlap by %v at tick %d", i, j, depth, tick)
			}
		}
	}
}

func (rules *physics) runChecked(t *testing.T, runners []runner, from, to int64) {
	t.Helper()
	for tick := from; tick < to; tick++ {
		rules.step(runners, tick)
		assertNoRobotOverlap(t, rules, runners, tick)
	}
}

func near(a, b, accuracy float64) bool { return math.Abs(a-b) <= accuracy }

func TestRobotLandsOnAnotherRobotsHeadAndCanFlipFromIt(t *testing.T) {
	rules := solo()
	bottom := spawnAlone(GravityDown, 420)
	top := bottom
	top.y += 90
	runners := []runner{top, bottom}
	rules.runChecked(t, runners, 0, 60)
	if !near(rules.size.body(&runners[0]).minY, rules.size.body(&runners[1]).maxY, 1e-9) || runners[0].x != runners[1].x ||
		!runners[0].running() || !runners[1].running() {
		t.Fatalf("runners = %+v", runners)
	}
	if rules.supportedAlone(&runners[0]) || !rules.supported(runners, 0) {
		t.Fatal("the head below is the only surface")
	}
	if rules.flip(runners, 0) != flipped {
		t.Fatal("flips from the head")
	}
}

func TestOppositeGravityRobotsMeetSoleToSoleAndRunOnTogether(t *testing.T) {
	rules := solo()
	floor, ceiling := spawnAlone(GravityDown, 420), spawnAlone(GravityUp, 420)
	if rules.flipAlone(&floor) != flipped || rules.flipAlone(&ceiling) != flipped {
		t.Fatal("both flip from the rails")
	}
	runners := []runner{floor, ceiling}
	rules.runChecked(t, runners, 0, 90)
	lower, upper := rules.size.body(&runners[0]), rules.size.body(&runners[1])
	if runners[0].gravity != GravityUp || runners[1].gravity != GravityDown || !near(lower.maxY, upper.minY, 1e-6) ||
		!(lower.minY > floorSurface+50) || !(upper.maxY < ceilingSurface-50) || !near(runners[0].x, 420+runSpeed*0.75, 1e-6) ||
		!rules.supported(runners, 0) || !rules.supported(runners, 1) {
		t.Fatalf("runners = %+v", runners)
	}
	// The upper one flips to the ceiling; the lower one, pulled up too,
	// follows and ends up hanging from its head.
	if rules.flip(runners, 1) != flipped {
		t.Fatal("flip from the other robot's soles")
	}
	rules.runChecked(t, runners, 90, 150)
	if !near(rules.size.body(&runners[1]).maxY, ceilingSurface, 1e-6) ||
		!near(rules.size.body(&runners[0]).maxY, ceilingSurface-runnerHeight, 1e-6) {
		t.Fatalf("runners = %+v", runners)
	}
}

func TestFasterRobotBehindIsHeldBack(t *testing.T) {
	rules := solo()
	front, rear := spawnAlone(GravityDown, 500), spawnAlone(GravityDown, 460)
	rear.boostedUntilTick = 200
	for _, runners := range [][]runner{{front, rear}, {rear, front}} {
		rules.runChecked(t, runners, 0, 90)
		first, last := &runners[0], &runners[1]
		if first.x < last.x {
			first, last = last, first
		}
		ahead, behind := rules.size.body(first), rules.size.body(last)
		// Never pushed; nose to tail, at most one tick of travel apart (when
		// the rear one moved first).
		if !near(first.x, 500+runSpeed*0.75, 1e-6) || !(behind.maxX <= ahead.minX+1e-6) || !(behind.maxX > ahead.minX-2) {
			t.Fatalf("runners = %+v", runners)
		}
	}
}

func TestFinishedOrCrashedRobotsNoLongerCollide(t *testing.T) {
	rules := solo()
	gone := spawnAlone(GravityDown, 500)
	crash(&gone, EliminationBoundary, 0)
	runners := []runner{gone, spawnAlone(GravityDown, 480)}
	rules.step(runners, 0)
	if !near(runners[1].x, 480+runSpeed*tickDuration, 1e-9) {
		t.Fatalf("x = %v", runners[1].x)
	}
}

func TestRobotsLeaveTheirPlatformsAndLandOnTheRails(t *testing.T) {
	rules := newRacePhysics(2)
	runners := startRunners(2)
	rules.runChecked(t, runners, 0, 120)
	if !runners[0].running() || !runners[1].running() || !near(rules.size.body(&runners[0]).minY, floorSurface, 1e-6) ||
		!near(rules.size.body(&runners[1]).maxY, ceilingSurface, 1e-6) {
		t.Fatalf("runners = %+v", runners)
	}
}

// Online has no bots, so in place of the demo race every seat taps at seeded
// random moments and robots meet in every way. The reference engine does not
// promise "never inside each other" here: a robot wedged between a block and
// another robot, or landing from a portal onto one, is pushed out of the solid
// last (TestSwiftParityTrace pins that this port does the same). What holds is
// that no robot ever ends inside a solid and an overlap between robots never
// outlives one tick.
func TestSeededRacesKeepRobotsApart(t *testing.T) {
	for count := 2; count <= MaximumPlayers; count++ {
		for seed := range uint64(20) {
			random := rand.New(rand.NewPCG(uint64(count), seed))
			rules := newRacePhysics(count)
			runners := startRunners(count)
			contacts, overlapping := 0, false
			for tick := int64(0); tick < MaximumMatchTicks && anyRunning(runners); tick++ {
				for seat := range runners {
					if random.IntN(40) == 0 {
						rules.flip(runners, seat)
					}
				}
				rules.step(runners, tick)
				overlaps := robotOverlaps(&rules, runners)
				if overlaps && overlapping {
					t.Fatalf("%d robots, seed %d: robots overlap two ticks running at tick %d", count, seed, tick)
				}
				overlapping = overlaps
				assertClearOfSolids(t, &rules, runners, tick)
				contacts += touching(&rules, runners)
			}
			if contacts == 0 {
				t.Fatalf("%d robots, seed %d: robots never met", count, seed)
			}
		}
	}
}

func robotOverlaps(rules *physics, runners []runner) bool {
	for i := range runners {
		for j := i + 1; j < len(runners); j++ {
			if runners[i].running() && runners[j].running() &&
				overlapDepth(rules.size.body(&runners[i]), rules.size.body(&runners[j])) >= 1e-6 {
				return true
			}
		}
	}
	return false
}

func assertClearOfSolids(t *testing.T, rules *physics, runners []runner, tick int64) {
	t.Helper()
	for seat := range runners {
		if !runners[seat].running() {
			continue
		}
		body := rules.size.body(&runners[seat])
		rules.course.visitSolidsNear(body, func(wall *solid) {
			if depth := overlapDepth(wall.box, body); depth >= 1e-6 {
				t.Fatalf("robot %d is %v inside solid %d at tick %d", seat, depth, wall.id, tick)
			}
		})
	}
}

func touching(rules *physics, runners []runner) (contacts int) {
	for i := range runners {
		for j := i + 1; j < len(runners); j++ {
			if !runners[i].running() || !runners[j].running() {
				continue
			}
			one, two := rules.size.body(&runners[i]), rules.size.body(&runners[j])
			if one.minX <= two.maxX+1e-6 && two.minX <= one.maxX+1e-6 && one.minY <= two.maxY+1e-6 && two.minY <= one.maxY+1e-6 {
				contacts++
			}
		}
	}
	return contacts
}
