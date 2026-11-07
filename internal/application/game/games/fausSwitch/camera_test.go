package fausSwitch

import (
	"math"
	"testing"
)

// FausSwitchTrackTests: the trail tightens through the first bend, widens
// through the second and never goes back.
func TestCameraTrailFollowsTheBends(t *testing.T) {
	path := standardCourse
	first, second := path.track.turns[0], path.track.turns[1]
	if path.cameraTrail(0) != 0 {
		t.Fatalf("trail at tick 0 = %v", path.cameraTrail(0))
	}
	for x, want := range map[float64]float64{first.startX - 1: 380, first.endX() + 1: 140, second.startX - 1: 140, second.endX() + 1: 380} {
		if got := behindAt(x, path.track); got != want {
			t.Fatalf("behind at %v = %v, want %v", x, got, want)
		}
	}
	if aheadAt(first.startX-1, path.track) != 464 || aheadAt(first.endX()+1, path.track) != 380 {
		t.Fatal("view ahead is 464 horizontally and 380 on the climb")
	}
	previous := math.Inf(-1)
	for tick := int64(0); tick <= MaximumMatchTicks; tick += 7 {
		trail := path.cameraTrail(tick)
		if trail < previous {
			t.Fatalf("trail went back at tick %d", tick)
		}
		previous = trail
	}
	// At the end the camera stops with the finish line in view.
	last := path.cameraFocus(MaximumMatchTicks)
	if last != path.length-(viewportWidth-horizontalBehind) || last != 19_936 || !(path.finishX < last+viewportWidth-horizontalBehind) {
		t.Fatalf("last focus = %v", last)
	}
	// On the first straight the trail is the old flat course: 225 · t / 120.
	if path.cameraTrail(1_200) != 2_250 {
		t.Fatalf("trail at tick 1200 = %v", path.cameraTrail(1_200))
	}
}

// The shared camera table holds exactly what the formulas give, and the
// course reads the formulas outside the table.
func TestCameraTableMatchesTheFormulas(t *testing.T) {
	path := standardCourse
	same := func(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }
	for tick := int64(-3); tick <= MaximumMatchTicks+5; tick++ {
		focus := focusAt(tick, path.length, path.track)
		if !same(path.cameraFocus(tick), focus) || !same(path.cameraTrail(tick), focus-behindAt(focus, path.track)) {
			t.Fatalf("tick %d: focus or trail differ", tick)
		}
		for _, front := range []float64{focus - 300, focus - 1, focus, focus + 1, focus + 120} {
			gap := focus - front
			want := gap * keepAt(tick, path.length, path.track, aheadAt)
			if gap > 0 {
				want = gap * keepAt(tick, path.length, path.track, behindAt)
			}
			if !same(path.turnCarry(front, tick), want) {
				t.Fatalf("tick %d front %v: carry %v, want %v", tick, front, path.turnCarry(front, tick), want)
			}
		}
	}
	if len(path.camera.focus) != MaximumMatchTicks+2 || len(path.camera.behindKeep) != MaximumMatchTicks+1 {
		t.Fatal("table covers every step of a race")
	}
}

// FausSwitchTrackTests.testTurnsKeepEachRobotsShareOfTheMargin.
func TestTurnsKeepEachRobotsShareOfTheMargin(t *testing.T) {
	rules := physics{course: standardCourse, size: newRunnerSize(1)}
	path := standardCourse
	first, second := path.track.turns[0], path.track.turns[1]
	if path.turnCarry(2_000, 2_000) != 0 {
		t.Fatal("no carry on a straight")
	}
	for _, share := range []float64{0.2, 0.79, 0.97} {
		for _, gravity := range []Gravity{GravityDown, GravityUp} {
			tick := ticksOf((first.startX + 250 - spawnX) / runSpeed)
			focus := path.cameraFocus(tick)
			robot := spawnAlone(gravity, 0)
			robot.x = focus - share*behindAt(focus, path.track) - runnerWidth/2
			now := tick
			for path.cameraFocus(now) < first.endX()+20 {
				rules.stepAlone(&robot, now)
				now++
				focus := path.cameraFocus(now)
				if !robot.running() || math.Abs((focus-rules.size.body(&robot).maxX)/behindAt(focus, path.track)-share) > 0.01 {
					t.Fatalf("share %v %v: lost the margin share at tick %d", share, gravity, now)
				}
			}
			if math.Abs(path.cameraFocus(now)-rules.size.body(&robot).maxX-share*140) > 4 {
				t.Fatalf("share %v enters the climb at the wrong distance", share)
			}
		}
	}
	tick := ticksOf((second.startX - 100 - spawnX) / runSpeed)
	robot := spawnAlone(GravityDown, 0)
	robot.x = path.cameraFocus(tick) - 0.79*140 - runnerWidth/2
	now := tick
	for path.cameraFocus(now) < second.endX()+20 {
		rules.stepAlone(&robot, now)
		now++
		if !robot.running() {
			t.Fatal("the second bend alone eliminated the robot")
		}
	}
	if math.Abs(path.cameraFocus(now)-rules.size.body(&robot).maxX-0.79*380) > 4 {
		t.Fatal("the distance comes back through the second bend")
	}
	// A robot ahead of the focus keeps its share of the view ahead.
	start := ticksOf((first.startX - 300 - spawnX) / runSpeed)
	leader := spawnAlone(GravityDown, 0)
	leader.x = path.cameraFocus(start) + 100 - runnerWidth/2
	now = start
	for path.cameraFocus(now) < first.endX()+20 {
		rules.stepAlone(&leader, now)
		now++
	}
	if math.Abs(rules.size.body(&leader).maxX-path.cameraFocus(now)-100*verticalAhead/horizontalAhead) > 3 {
		t.Fatal("the leader lost its share of the view ahead")
	}
}
