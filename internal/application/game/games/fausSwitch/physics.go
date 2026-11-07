package fausSwitch

import "math"

// Physics (FausSwitchPhysics.swift) at a fixed 120 Hz step. Robots are solid
// to each other: a race steps them one at a time in seat order; each moves,
// then is pushed out of the robots (where they are at that moment) and the
// course. Finished and crashed robots no longer collide. Briefing §4.4 lists
// the step; the comments below follow its numbering.

// runner is FausSwitchRunnerState without the scale, which belongs to the
// race (runnerSize). It has no pointers, so copying a world is one memcpy.
type runner struct {
	// x and y are the body center.
	x, y, velocityY float64
	gravity         Gravity
	status          RunnerStatus
	// crashReason, finishedAtTick and crashedAtTick are meaningful only for
	// the matching status.
	crashReason      EliminationReason
	boostedUntilTick int64
	slowedUntilTick  int64
	finishedAtTick   int64
	crashedAtTick    int64
	usedBoosts       padSet
	usedSlows        padSet
}

func (robot *runner) running() bool { return robot.status == RunnerRunning }

// spawnAlone stands a full-size robot on the floor (down) or hangs it from the
// ceiling (up). Races start from startLanes; this is for single-robot checks.
func spawnAlone(gravity Gravity, x float64) runner {
	half := runnerHeight / 2
	y := ceilingSurface - half
	if gravity == GravityDown {
		y = floorSurface + half
	}
	return runner{x: x, y: y, gravity: gravity, status: RunnerRunning}
}

// runnerSize is a race's robot size. Swift multiplies the full size by the
// scale at run time for every body; computing the same products once gives the
// same float64 values.
type runnerSize struct {
	width, height         float64
	halfWidth, halfHeight float64
	// probeInset keeps the support probe inside the body's sides.
	probeInset float64
}

func newRunnerSize(scale float64) runnerSize {
	width, height := runnerWidth*scale, runnerHeight*scale
	return runnerSize{width: width, height: height, halfWidth: width / 2, halfHeight: height / 2,
		probeInset: swiftMin(maximumSupportInset, width*supportInsetShare)}
}

func (size runnerSize) body(robot *runner) rect {
	return rect{minX: robot.x - size.halfWidth, minY: robot.y - size.halfHeight, maxX: robot.x + size.halfWidth, maxY: robot.y + size.halfHeight}
}

// supportProbe is the thin box under the feet (toward gravity) that must touch
// a solid or another running robot for the robot to flip.
func (size runnerSize) supportProbe(robot *runner) rect {
	edge := robot.y + float64(robot.gravity.sign()*size.halfHeight)
	return rect{minX: robot.x - size.halfWidth + size.probeInset, minY: edge - supportProbeReach,
		maxX: robot.x + size.halfWidth - size.probeInset, maxY: edge + supportProbeReach}
}

// robots are the bodies of a race's running robots by seat, as the stepping
// robot sees them.
type robots struct {
	bodies  [MaximumPlayers]rect
	running [MaximumPlayers]bool
	count   int
}

func (others *robots) set(seat int, robot *runner, size runnerSize) {
	others.running[seat] = robot.running()
	if others.running[seat] {
		others.bodies[seat] = size.body(robot)
	}
}

// physics is one race's rules: its course and its robots' size.
type physics struct {
	course *course
	size   runnerSize
}

// newRacePhysics is the physics of a race that started with runnerCount robots.
func newRacePhysics(runnerCount int) physics {
	return physics{course: raceCourse(runnerCount), size: newRunnerSize(runnerScale(runnerCount))}
}

func (rules *physics) robotsOf(runners []runner) robots {
	others := robots{count: len(runners)}
	for seat := range runners {
		others.set(seat, &runners[seat], rules.size)
	}
	return others
}

// supportedAmong: the probe touches a solid or another running robot.
func (rules *physics) supportedAmong(robot *runner, others *robots, skip int) bool {
	probe := rules.size.supportProbe(robot)
	for seat := range others.count {
		if seat != skip && others.running[seat] && others.bodies[seat].intersects(probe) {
			return true
		}
	}
	return rules.course.overlapsSolid(probe)
}

// supported reports whether the robot at seat may flip in this race.
func (rules *physics) supported(runners []runner, seat int) bool {
	others := rules.robotsOf(runners)
	return rules.supportedAmong(&runners[seat], &others, seat)
}

type flipOutcome int8

const (
	flipped flipOutcome = iota + 1
	// flipUnsupported: mid-air, the tap is ignored.
	flipUnsupported
	flipNotRunning
)

// flip reverses gravity and launches toward the new surface; only a supported
// running robot flips.
func (rules *physics) flip(runners []runner, seat int) flipOutcome {
	robot := &runners[seat]
	return flipIf(robot, robot.running() && rules.supported(runners, seat))
}

func flipIf(robot *runner, supported bool) flipOutcome {
	if !robot.running() {
		return flipNotRunning
	}
	if !supported {
		return flipUnsupported
	}
	robot.gravity = robot.gravity.opposite()
	launch := swiftMax(switchVelocity, math.Abs(robot.velocityY)*flipCarry)
	robot.velocityY = robot.gravity.sign() * launch
	return flipped
}

// step advances every running robot from tick to tick+1 in seat order.
func (rules *physics) step(runners []runner, tick int64) {
	others := rules.robotsOf(runners)
	for seat := range runners {
		if !runners[seat].running() {
			continue
		}
		rules.stepRobot(&runners[seat], tick, &others, seat)
		others.set(seat, &runners[seat], rules.size)
	}
}

// stepAlone advances one robot with no other robots around.
func (rules *physics) stepAlone(robot *runner, tick int64) {
	var none robots
	rules.stepRobot(robot, tick, &none, -1)
}

func (rules *physics) stepRobot(robot *runner, tick int64, others *robots, skip int) {
	if !robot.running() {
		return
	}
	path := rules.course
	next := tick + 1
	// 1. Speed.
	speed := runSpeed
	if tick < robot.boostedUntilTick {
		speed += boostExtraSpeed
	}
	if tick < robot.slowedUntilTick {
		speed -= slowPenalty
	}
	// 2–3. Gravity and movement; in a bend the robot also keeps its share of
	// the camera margin (carry is 0 on straights), measured before the move.
	before := rules.size.body(robot)
	carry := path.turnCarry(before.maxX, tick)
	robot.velocityY += float64(robot.gravity.sign() * gravityStrength * tickDuration)
	robot.x += float64(speed*tickDuration) + carry
	robot.y += float64(robot.velocityY * tickDuration)
	// 4. Contacts.
	rules.resolveContacts(robot, before, others, skip)
	// 4b. A portal sends the robot back on the other rail, never behind the
	// trail, so a portal alone eliminates nobody.
	body := rules.size.body(robot)
	for index := range path.portals {
		door := &path.portals[index]
		if !door.opening().intersects(body) {
			continue
		}
		robot.x = door.landingX(path.cameraTrail(next))
		robot.y = door.exitY()
		robot.gravity = door.exitGravity()
		robot.velocityY = 0
		rules.resolveContacts(robot, rules.size.body(robot), others, skip)
		break
	}
	// 5. Support, after contacts.
	body = rules.size.body(robot)
	supported := rules.supportedAmong(robot, others, skip)
	// 6. A gap under an unsupported robot pushes it off the playfield.
	for index := range path.gaps {
		hole := &path.gaps[index]
		if hole.rail == robot.gravity && !supported && hole.sensor().intersects(body) {
			exit := swiftMax(math.Abs(robot.velocityY), gapExitSpeed)
			robot.velocityY = robot.gravity.sign() * exit
		}
	}
	// 7–8. Boost and slow pads, once per robot each.
	for index := range path.boosts {
		strip := &path.boosts[index]
		if !robot.usedBoosts.has(strip.id) && strip.box().intersects(body) {
			robot.usedBoosts.add(strip.id)
			robot.boostedUntilTick = max(robot.boostedUntilTick, next+boostTicks)
		}
	}
	for index := range path.slows {
		strip := &path.slows[index]
		if !robot.usedSlows.has(strip.id) && strip.box().intersects(body) {
			robot.usedSlows.add(strip.id)
			robot.slowedUntilTick = max(robot.slowedUntilTick, next+slowTicks)
		}
	}
	// 9. Finish.
	if body.maxX >= path.finishX-finishReach {
		robot.status = RunnerFinished
		robot.finishedAtTick = next
		robot.velocityY = 0
		return
	}
	// 10. Vertical boundary.
	if robot.y < -boundaryMargin || robot.y > worldHeight+boundaryMargin {
		crash(robot, EliminationBoundary, next)
		return
	}
	// 11. Left behind by the camera.
	if body.maxX <= path.cameraTrail(next) {
		crash(robot, EliminationLeftBehind, next)
	}
}

// crash eliminates a running robot.
func crash(robot *runner, reason EliminationReason, tick int64) {
	if !robot.running() {
		return
	}
	robot.status = RunnerCrashed
	robot.crashReason = reason
	robot.crashedAtTick = tick
	robot.velocityY = 0
}

// resolveContacts pushes the body out of each other running robot, then out of
// each solid, along the shallower overlap, for up to four passes (the course
// goes last so a robot never ends inside a rail). Against another robot the
// side it came from wins: if the body before the move was clear of it
// vertically (and not sideways) the push is vertical, and the other way round.
// A push against the vertical motion stops it (no bounce); sideways pushes only
// block progress. Only the stepping robot moves.
func (rules *physics) resolveContacts(robot *runner, before rect, others *robots, skip int) {
	for range contactPasses {
		moved := false
		body := rules.size.body(robot)
		push := func(other rect, sweep bool) {
			if !other.intersects(body) {
				return
			}
			overlapX := swiftMin(body.maxX-other.minX, other.maxX-body.minX)
			overlapY := swiftMin(body.maxY-other.minY, other.maxY-body.minY)
			if !(overlapX > contactEpsilon) || !(overlapY > contactEpsilon) {
				return
			}
			vertical := overlapY <= overlapX
			if sweep {
				clearX := before.maxX <= other.minX+contactEpsilon || before.minX >= other.maxX-contactEpsilon
				clearY := before.maxY <= other.minY+contactEpsilon || before.minY >= other.maxY-contactEpsilon
				if clearY != clearX {
					vertical = clearY
				}
			}
			switch {
			case vertical && body.midY() >= other.midY():
				robot.y += other.maxY - body.minY
				if robot.velocityY < 0 {
					robot.velocityY = 0
				}
			case vertical:
				robot.y -= body.maxY - other.minY
				if robot.velocityY > 0 {
					robot.velocityY = 0
				}
			case body.midX() <= other.midX():
				robot.x -= body.maxX - other.minX
			default:
				robot.x += other.maxX - body.minX
			}
			body = rules.size.body(robot)
			moved = true
		}
		for seat := range others.count {
			if seat != skip && others.running[seat] {
				push(others.bodies[seat], true)
			}
		}
		rules.course.visitSolidsNear(body, func(wall *solid) { push(wall.box, false) })
		if !moved {
			return
		}
	}
}

// ranks is FausSwitchRanking.ranks by seat: finishers first by finish tick,
// then robots still running by distance, then crashes, the later the better.
// Equal results share a rank (1, 1, 3).
func ranks(runners []runner) []int {
	type standing struct {
		group int
		value float64
	}
	key := func(robot *runner) standing {
		switch robot.status {
		case RunnerFinished:
			return standing{0, float64(robot.finishedAtTick)}
		case RunnerRunning:
			return standing{1, -robot.x}
		}
		return standing{2, -float64(robot.crashedAtTick)}
	}
	result := make([]int, len(runners))
	for seat := range runners {
		mine := key(&runners[seat])
		result[seat] = 1
		for other := range runners {
			theirs := key(&runners[other])
			if theirs.group < mine.group || (theirs.group == mine.group && theirs.value < mine.value) {
				result[seat]++
			}
		}
	}
	return result
}
