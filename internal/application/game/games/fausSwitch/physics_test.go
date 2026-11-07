package fausSwitch

import (
	"math"
	"slices"
	"testing"
)

// ticksOf is FausSwitchMetrics.ticks.
func ticksOf(seconds float64) int64 { return int64(math.Round(seconds * PhysicsRateHz)) }

// startTick is the tick at which the camera focus reaches x.
func startTick(x float64) int64 { return ticksOf((x - spawnX) / runSpeed) }

// solo is a full-size robot alone on the standard course, as
// FausSwitchPhysicsTests runs it.
func solo() physics { return physics{course: standardCourse, size: newRunnerSize(1)} }

func (rules *physics) runAlone(robot *runner, from int64, ticks int) int64 {
	tick := from
	for range ticks {
		rules.stepAlone(robot, tick)
		tick++
	}
	return tick
}

func (rules *physics) supportedAlone(robot *runner) bool {
	var none robots
	return rules.supportedAmong(robot, &none, -1)
}

func (rules *physics) flipAlone(robot *runner) flipOutcome {
	return flipIf(robot, rules.supportedAlone(robot))
}

func firstFloorWall(t *testing.T) rect {
	t.Helper()
	for _, wall := range standardCourse.solids {
		if wall.kind == solidBlock && wall.box.minY <= 0 {
			return wall.box
		}
	}
	t.Fatal("no floor wall")
	return rect{}
}

func TestGravityAndRobotSize(t *testing.T) {
	if GravityDown.opposite() != GravityUp || GravityUp.opposite().opposite() != GravityUp || GravityDown.sign() != -1 || GravityUp.sign() != 1 {
		t.Fatal("gravity")
	}
	if math.Abs(runnerWidth-34*0.8) > 1e-4 || math.Abs(runnerHeight-42*0.8) > 1e-4 {
		t.Fatal("robot is 80 % of the solo house")
	}
}

func TestRunKeepsConstantSpeedAndRestsOnTheFloor(t *testing.T) {
	rules := solo()
	robot := spawnAlone(GravityDown, spawnX)
	rules.runAlone(&robot, 0, 120)
	if !robot.running() || math.Abs(robot.x-(spawnX+runSpeed)) > 0.01 || math.Abs(rules.size.body(&robot).minY-floorSurface) > 1e-4 ||
		!rules.supportedAlone(&robot) {
		t.Fatalf("robot = %+v", robot)
	}
}

func TestFlipNeedsASurfaceAndLaunchesWithTheSoloVelocity(t *testing.T) {
	rules := solo()
	robot := spawnAlone(GravityDown, spawnX)
	if rules.flipAlone(&robot) != flipped || robot.gravity != GravityUp || robot.velocityY != switchVelocity {
		t.Fatalf("robot = %+v", robot)
	}
	rules.stepAlone(&robot, 0)
	if rules.flipAlone(&robot) != flipUnsupported || robot.gravity != GravityUp {
		t.Fatal("mid-air taps are ignored")
	}
	crash(&robot, EliminationBoundary, 1)
	if flipIf(&robot, true) != flipNotRunning {
		t.Fatal("a crashed robot never flips")
	}
}

func TestFlipCrossesThePlayfieldInAboutPointFourSeconds(t *testing.T) {
	rules := solo()
	robot := spawnAlone(GravityDown, spawnX)
	rules.flipAlone(&robot)
	tick := int64(0)
	for !rules.supportedAlone(&robot) || rules.size.body(&robot).maxY < ceilingSurface-0.01 {
		rules.stepAlone(&robot, tick)
		tick++
		if tick >= 120 {
			t.Fatal("the flip never landed")
		}
	}
	if math.Abs(seconds(tick)-0.375) > 0.02 || robot.velocityY != 0 {
		t.Fatalf("landed after %v s with %v", seconds(tick), robot.velocityY)
	}
}

func TestFlipKeepsAShareOfAFastVerticalSpeed(t *testing.T) {
	rules := solo()
	robot := runner{x: 500, y: floorSurface + runnerHeight/2, velocityY: -1_200, gravity: GravityDown, status: RunnerRunning}
	if rules.flipAlone(&robot) != flipped || math.Abs(robot.velocityY-1_200*flipCarry) > 1e-4 {
		t.Fatalf("velocity %v", robot.velocityY)
	}
}

func TestSolidFrontFaceBlocksButNeverKills(t *testing.T) {
	rules := solo()
	wall := firstFloorWall(t)
	robot := spawnAlone(GravityDown, wall.minX-40)
	rules.runAlone(&robot, startTick(robot.x), 60)
	if !robot.running() || math.Abs(rules.size.body(&robot).maxX-wall.minX) > 1e-3 || !rules.supportedAlone(&robot) {
		t.Fatalf("robot = %+v", robot)
	}
}

func TestBlockedRobotIsLostOnlyAfterLeavingTheCameraCompletely(t *testing.T) {
	rules := solo()
	wall := firstFloorWall(t)
	robot := spawnAlone(GravityDown, wall.minX-20)
	tick := startTick(robot.x)
	for robot.running() {
		rules.stepAlone(&robot, tick)
		tick++
	}
	body := rules.size.body(&robot)
	if robot.crashReason != EliminationLeftBehind || robot.crashedAtTick != tick ||
		!(body.maxX <= standardCourse.cameraTrail(tick)) || !(body.maxX > standardCourse.cameraTrail(tick-1)) {
		t.Fatalf("robot = %+v", robot)
	}
}

func TestFloorGapDropsARobotOffScreen(t *testing.T) {
	rules := solo()
	robot := spawnAlone(GravityDown, standardCourse.gaps[0].minX-30)
	tick := startTick(robot.x)
	for robot.running() && tick < 100_000 {
		rules.stepAlone(&robot, tick)
		tick++
	}
	if robot.crashReason != EliminationBoundary || robot.velocityY != 0 {
		t.Fatalf("robot = %+v", robot)
	}
}

func TestRunningAlongARailNeverTouchesABoost(t *testing.T) {
	rules := solo()
	for _, rail := range []Gravity{GravityDown, GravityUp} {
		robot := spawnAlone(rail, standardCourse.boosts[0].x-120)
		rules.runAlone(&robot, startTick(robot.x), 80)
		if robot.usedBoosts != 0 {
			t.Fatalf("%v rail touched a boost", rail)
		}
	}
}

func TestBoostPadWorksOncePerRobot(t *testing.T) {
	rules := solo()
	strip := standardCourse.boosts[0]
	robot := runner{x: strip.x - 40, y: strip.y + 40, gravity: GravityDown, status: RunnerRunning}
	tick := startTick(robot.x)
	boosted := int64(0)
	for range 200 {
		rules.stepAlone(&robot, tick)
		tick++
		if tick < robot.boostedUntilTick {
			boosted++
		}
	}
	if !slices.Equal(robot.usedBoosts.ids(), []int{strip.id}) || boosted != boostTicks {
		t.Fatalf("boosts %v for %d ticks", robot.usedBoosts.ids(), boosted)
	}
}

func TestSlowPadSlowsOncePerRobot(t *testing.T) {
	rules := solo()
	var strip pad
	for _, candidate := range standardCourse.slows {
		if candidate.y < 100 {
			strip = candidate
			break
		}
	}
	start := strip.x - 60
	slowed, free := spawnAlone(GravityDown, start), spawnAlone(GravityUp, start)
	rules.runAlone(&slowed, startTick(start), 130)
	rules.runAlone(&free, startTick(start), 20)
	if !slices.Equal(slowed.usedSlows.ids(), []int{strip.id}) || free.usedSlows != 0 {
		t.Fatalf("slowed %v free %v", slowed.usedSlows.ids(), free.usedSlows.ids())
	}
	lost := slowPenalty * seconds(slowTicks)
	if math.Abs(start+runSpeed*seconds(130)-slowed.x-lost) > 0.01 {
		t.Fatalf("lost %v, want %v", start+runSpeed*seconds(130)-slowed.x, lost)
	}
}

func TestPortalSendsTheRobotBackOnTheOtherRail(t *testing.T) {
	rules := solo()
	for _, door := range standardCourse.portals {
		robot := spawnAlone(door.rail, door.x-40)
		tick := startTick(robot.x)
		teleported := false
		for range 40 {
			before := robot.x
			rules.stepAlone(&robot, tick)
			tick++
			if robot.x < before {
				teleported = true
				if math.Abs(robot.x-door.exitX) > 1e-9 || robot.gravity != door.rail.opposite() || robot.velocityY != 0 || !rules.supportedAlone(&robot) {
					t.Fatalf("portal %d landing %+v", door.id, robot)
				}
				break
			}
		}
		if !teleported || !robot.running() {
			t.Fatalf("portal %d did not catch a robot on its rail", door.id)
		}
		rules.runAlone(&robot, tick, 120)
		if !robot.running() || robot.gravity != door.rail.opposite() {
			t.Fatalf("portal %d exit is not safe", door.id)
		}
	}
}

func TestPortalLeavesTheOtherRailAlone(t *testing.T) {
	rules := solo()
	door := standardCourse.portals[0]
	robot := spawnAlone(door.rail.opposite(), door.x-40)
	rules.runAlone(&robot, startTick(robot.x), 60)
	if !(robot.x > door.x+40) || robot.gravity != door.rail.opposite() {
		t.Fatalf("robot = %+v", robot)
	}
}

func TestPortalNeverDropsARobotBehindTheTrail(t *testing.T) {
	rules := solo()
	turns := standardCourse.track.turns
	var door portal
	for _, candidate := range standardCourse.portals {
		if candidate.x > turns[0].endX() && candidate.x < turns[1].startX {
			door = candidate
			break
		}
	}
	robot := spawnAlone(door.rail, door.x-30)
	focus := rules.size.body(&robot).maxX + 120
	tick := startTick(focus)
	// Swift nests this test inside testPortalIsJustBigEnoughForARobot, so XCTest
	// never runs it, and its "x went back" check misses this very landing: here
	// the trail pulls the robot forward of where it entered. The rail switch is
	// what marks the teleport.
	landed := false
	for range 40 {
		rules.stepAlone(&robot, tick)
		tick++
		if robot.gravity != door.rail {
			landed = true
			if !(robot.x > door.exitX) || !(rules.size.body(&robot).maxX > standardCourse.cameraTrail(tick)) {
				t.Fatalf("the robot must land in front of the fixed exit and the trail: %+v", robot)
			}
			break
		}
	}
	rules.runAlone(&robot, tick, 60)
	if !landed || !robot.running() {
		t.Fatalf("landed %v robot %+v", landed, robot)
	}
}

func TestRankingOrdersFinishersThenRunnersThenLaterCrashes(t *testing.T) {
	finished := func(tick int64) runner { return runner{status: RunnerFinished, finishedAtTick: tick} }
	crashed := func(tick int64) runner { return runner{status: RunnerCrashed, crashedAtTick: tick} }
	running := func(x float64) runner { return runner{status: RunnerRunning, x: x} }
	got := ranks([]runner{crashed(500), finished(4_000), finished(4_000), crashed(900)})
	if !slices.Equal(got, []int{4, 1, 1, 3}) {
		t.Fatalf("ranks = %v", got)
	}
	got = ranks([]runner{running(900), crashed(2), finished(9), running(1_200), running(900)})
	if !slices.Equal(got, []int{3, 5, 1, 2, 3}) {
		t.Fatalf("ranks = %v", got)
	}
}

func TestPadSet(t *testing.T) {
	var set padSet
	if ids := set.ids(); ids == nil || len(ids) != 0 {
		t.Fatal("an empty set is an empty, non-nil list")
	}
	for _, id := range []int{13, 0, 8, 0} {
		set.add(id)
	}
	if !slices.Equal(set.ids(), []int{0, 8, 13}) || !set.has(8) || set.has(1) {
		t.Fatalf("ids = %v", set.ids())
	}
}

// The step never allocates: a rewind replays it thousands of times a second.
func TestStepDoesNotAllocate(t *testing.T) {
	rules := newRacePhysics(MaximumPlayers)
	runners := startRunners(MaximumPlayers)
	tick := int64(0)
	allocations := testing.AllocsPerRun(200, func() {
		if tick%37 == 0 {
			rules.flip(runners, int(tick/37)%MaximumPlayers)
		}
		rules.step(runners, tick)
		tick++
	})
	if allocations != 0 {
		t.Fatalf("%v allocations per step", allocations)
	}
}

func startRunners(count int) []runner {
	lanes := startLanes(count, runnerScale(count))
	runners := make([]runner, count)
	for seat, start := range lanes {
		runners[seat] = start.spawn
	}
	return runners
}

func BenchmarkStep8(b *testing.B) {
	rules := newRacePhysics(MaximumPlayers)
	runners := startRunners(MaximumPlayers)
	tick := int64(0)
	b.ReportAllocs()
	for b.Loop() {
		if tick%29 == 0 {
			rules.flip(runners, int(tick/29)%MaximumPlayers)
		}
		rules.step(runners, tick)
		tick++
		if tick == 3_000 {
			runners, tick = startRunners(MaximumPlayers), 0
		}
	}
}
