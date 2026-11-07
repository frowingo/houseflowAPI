package fausSwitch

// The camera rules physics depends on (FausSwitchCamera): the focus moves at
// run speed from the start line; a robot whose body is wholly behind the trail
// (the focus minus behind) is left behind. The trail keeps horizontalBehind of
// the run on horizontal stretches and verticalBehind on the climb, eased
// through each bend. Drawing (zoom, view bounds) stays on the client.

// focusAt is FausSwitchCamera.focus: it stops once the finish is in view.
func focusAt(tick int64, length float64, path track) float64 {
	ahead := horizontalAhead
	if path.vertical[len(path.vertical)-1] {
		ahead = verticalAhead
	}
	return swiftMin(spawnX+float64(runSpeed*seconds(tick)), length-ahead)
}

// behindAt is how far behind the focus the trail is at course x.
func behindAt(x float64, path track) float64 {
	return blendedAt(x, path, horizontalBehind, verticalBehind)
}

// aheadAt is how far ahead of the focus the view reaches at course x.
func aheadAt(x float64, path track) float64 {
	return blendedAt(x, path, horizontalAhead, verticalAhead)
}

func blendedAt(x float64, path track, horizontal, vertical float64) float64 {
	value := horizontal
	if path.vertical[0] {
		value = vertical
	}
	for index := range path.turns {
		next := horizontal
		if path.vertical[index+1] {
			next = vertical
		}
		value += float64((next - value) * path.turnBlend(index, x))
	}
	return value
}

// keepAt is the factor of FausSwitchCamera.turnCarry for a body front behind
// the focus (behind) or ahead of it: carry = gap × keep, with gap = focus −
// front. It is exactly 0 on straight stretches.
func keepAt(tick int64, length float64, path track, measure func(float64, track) float64) float64 {
	now := measure(focusAt(tick, length, path), path)
	return 1 - measure(focusAt(tick+1, length, path), path)/now
}

// cameraTable holds the camera values of every tick of a race, computed once
// with the formulas above and shared read-only by every session. A rewind
// replays up to thirty ticks per flip, so the step reads them instead of
// re-evaluating the bends. Each entry is the same float64 the formula gives.
type cameraTable struct {
	// focus and trail cover ticks 0 … lastTick+1, behindKeep and aheadKeep
	// the steps from ticks 0 … lastTick.
	focus, trail          []float64
	behindKeep, aheadKeep []float64
}

func newCameraTable(length float64, path track, lastTick int64) *cameraTable {
	table := &cameraTable{
		focus:      make([]float64, lastTick+2),
		trail:      make([]float64, lastTick+2),
		behindKeep: make([]float64, lastTick+1),
		aheadKeep:  make([]float64, lastTick+1),
	}
	for tick := range int64(len(table.focus)) {
		focus := focusAt(tick, length, path)
		table.focus[tick] = focus
		table.trail[tick] = focus - behindAt(focus, path)
	}
	for tick := range int64(len(table.behindKeep)) {
		table.behindKeep[tick] = keepAt(tick, length, path, behindAt)
		table.aheadKeep[tick] = keepAt(tick, length, path, aheadAt)
	}
	return table
}

// cameraFocus is the course x the camera follows at tick.
func (path *course) cameraFocus(tick int64) float64 {
	if tick >= 0 && tick < int64(len(path.camera.focus)) {
		return path.camera.focus[tick]
	}
	return focusAt(tick, path.length, path.track)
}

// cameraTrail is the view's trailing edge at tick (FausSwitchCourse.cameraTrail).
func (path *course) cameraTrail(tick int64) float64 {
	if tick >= 0 && tick < int64(len(path.camera.trail)) {
		return path.camera.trail[tick]
	}
	focus := focusAt(tick, path.length, path.track)
	return focus - behindAt(focus, path.track)
}

// turnCarry is FausSwitchCamera.turnCarry: the extra course x a running robot
// whose body front is at front gets for the step from tick to tick+1, so it
// keeps its share of the margin behind the focus (or of the view ahead of it)
// while a bend changes them.
func (path *course) turnCarry(front float64, tick int64) float64 {
	gap := path.cameraFocus(tick) - front
	inTable := tick >= 0 && tick < int64(len(path.camera.behindKeep))
	if gap > 0 {
		if inTable {
			return gap * path.camera.behindKeep[tick]
		}
		return gap * keepAt(tick, path.length, path.track, behindAt)
	}
	if inTable {
		return gap * path.camera.aheadKeep[tick]
	}
	return gap * keepAt(tick, path.length, path.track, aheadAt)
}
