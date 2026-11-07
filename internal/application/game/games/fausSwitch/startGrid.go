package fausSwitch

import "math"

// Start grid (FausSwitchStartGrid). Every robot starts at the same start line
// on its own short platform. The band is split into one lane per robot; even
// seats stand on top of their platform with gravity down, odd seats hang under
// theirs with gravity up. From the floor up the lanes hold seats 0, 2, 4, 6,
// then 7, 5, 3, 1.

// lane is one seat's start: its gravity, platform and robot.
type lane struct {
	gravity  Gravity
	platform rect
	spawn    runner
}

func clampRunnerCount(count int) int { return min(MaximumPlayers, max(1, count)) }

// runnerScale is the robot size of a race of count robots, fixed when the
// race starts; the snapshot carries it as runnerScale (briefing §4.2.2).
func runnerScale(count int) float64 {
	if count > CrowdedRunnerThreshold {
		return CrowdedRunnerScale
	}
	return 1
}

// startLanes returns the lanes by seat. Heights are worked out in whole tenths
// of a unit with integer (floor) division so every platform agrees on them
// exactly; only the robot height depends on scale.
func startLanes(count int, scale float64) []lane {
	count = clampRunnerCount(count)
	tenths := func(value float64) int { return int(math.Round(value * 10)) }
	floor := tenths(floorSurface)          // 273
	band := tenths(ceilingSurface) - floor // 3354
	height := tenths(runnerHeight * scale) // 336, or 252 at 0.75
	thickness := tenths(platformThickness) // 60
	halfBlock := (height + thickness) / 2  // 198, or 156 at 0.75
	lanes := make([]lane, count)
	for seat := range lanes {
		gravity, index := GravityDown, seat/2
		if seat%2 == 1 {
			gravity, index = GravityUp, count-1-seat/2
		}
		center := floor + band*(2*index+1)/(2*count)
		var bottom, spawnCenter int
		if gravity == GravityDown {
			bottom = center - halfBlock
			spawnCenter = bottom + thickness + height/2
		} else {
			bottom = center + halfBlock - thickness
			spawnCenter = bottom - height/2
		}
		lanes[seat] = lane{
			gravity: gravity,
			platform: rect{minX: spawnX - platformBack, minY: float64(bottom) / 10,
				maxX: spawnX + platformAhead, maxY: float64(bottom+thickness) / 10},
			spawn: runner{x: spawnX, y: float64(spawnCenter) / 10, gravity: gravity, status: RunnerRunning},
		}
	}
	return lanes
}
