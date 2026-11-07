package fausSwitch

import "slices"

// Course geometry (FausSwitchCourse.swift). Every rule runs in course
// coordinates: x along the course, y across the band, floor rail at the
// bottom. The bends of the track are drawing and camera only; physics treats
// the course as straight.

// rect is FausSwitchRect: an axis-aligned box, y growing toward the ceiling.
type rect struct{ minX, minY, maxX, maxY float64 }

func centeredRect(centerX, centerY, width, height float64) rect {
	return rect{minX: centerX - width/2, minY: centerY - height/2, maxX: centerX + width/2, maxY: centerY + height/2}
}

func (box rect) midX() float64 { return (box.minX + box.maxX) / 2 }
func (box rect) midY() float64 { return (box.minY + box.maxY) / 2 }

// intersects is strict: boxes that only touch do not intersect.
func (box rect) intersects(other rect) bool {
	return box.minX < other.maxX && other.minX < box.maxX && box.minY < other.maxY && other.minY < box.maxY
}

// depth is how far two boxes overlap on their shallower axis; 0 when apart.
func (box rect) depth(other rect) float64 {
	if !box.intersects(other) {
		return 0
	}
	return min(min(box.maxX, other.maxX)-max(box.minX, other.minX), min(box.maxY, other.maxY)-max(box.minY, other.minY))
}

type solidKind int8

const (
	// solidRail is a floor or ceiling rail segment between gaps.
	solidRail solidKind = iota + 1
	// solidBlock is an obstacle, on a rail or floating.
	solidBlock
	// solidPlatform is a robot's start platform (startGrid.go).
	solidPlatform
)

// solid is FausSwitchSolid: every face is safe to touch.
type solid struct {
	id   int
	kind solidKind
	box  rect
}

// portal is FausSwitchPortal: a doorway on one rail that sends a robot back to
// exitX on the other rail. It is not solid and never ends a run by itself.
type portal struct {
	id    int
	x     float64
	rail  Gravity
	exitX float64
}

func (door portal) opening() rect {
	half := portalOpeningWidth / 2
	if door.rail == GravityDown {
		return rect{minX: door.x - half, minY: floorSurface, maxX: door.x + half, maxY: floorSurface + portalHeight}
	}
	return rect{minX: door.x - half, minY: ceilingSurface - portalHeight, maxX: door.x + half, maxY: ceilingSurface}
}

// landingX is exitX, or just in front of the trail when exitX is behind it,
// but never at the portal itself. Full robot size, whatever the race scale.
func (door portal) landingX(trail float64) float64 {
	safe := trail + runnerWidth/2 + portalTrailRoom
	return swiftMin(swiftMax(door.exitX, safe), door.x-portalOpeningWidth/2-runnerWidth/2-1)
}

func (door portal) exitGravity() Gravity { return door.rail.opposite() }

// exitY stands a full-size robot on the opposite rail; a smaller robot appears
// slightly above it and falls.
func (door portal) exitY() float64 {
	half := runnerHeight / 2
	if door.exitGravity() == GravityDown {
		return floorSurface + half
	}
	return ceilingSurface - half
}

// gap is FausSwitchGap: a hole in one rail; a robot pulled toward that rail
// falls out through it.
type gap struct {
	id         int
	minX, maxX float64
	rail       Gravity
}

func (hole gap) sensor() rect {
	edge := ceilingSurface - gapSensorRise
	if hole.rail == GravityDown {
		edge = floorSurface + gapSensorRise
	}
	return rect{minX: hole.minX + gapSensorInset, minY: edge - gapSensorReach,
		maxX: swiftMax(hole.minX+gapSensorInset+gapSensorMinimumWidth, hole.maxX-gapSensorInset), maxY: edge + gapSensorReach}
}

// pad is a boost (FausSwitchBoostPad) or slow (FausSwitchSlowPad) strip; each
// robot can use each pad once.
type pad struct {
	id   int
	x, y float64
}

func (strip pad) box() rect { return centeredRect(strip.x, strip.y, padWidth, padHeight) }

// slowSource is a slow pad as the course data lists it.
type slowSource struct {
	id     int
	x, y   float64
	onRail Gravity
}

// pad resolves FausSwitchSlowPad.onRail: lying on the floor or hanging from
// the ceiling.
func (source slowSource) pad() pad {
	switch source.onRail {
	case GravityDown:
		return pad{id: source.id, x: source.x, y: floorSurface + padHeight/2}
	case GravityUp:
		return pad{id: source.id, x: source.x, y: ceilingSurface - padHeight/2}
	}
	return pad{id: source.id, x: source.x, y: source.y}
}

// padSet is the pads a robot already used, one bit per pad id.
type padSet uint16

const maximumPads = 16

func (set padSet) has(id int) bool { return set&(1<<id) != 0 }
func (set *padSet) add(id int)     { *set |= 1 << id }

// ids are ascending, never nil (the wire writes an empty array).
func (set padSet) ids() []int {
	ids := []int{}
	for id := 0; id < maximumPads; id++ {
		if set.has(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// course is FausSwitchCourse. Courses are built once at start-up and shared
// read-only by every session.
type course struct {
	version         int
	length, finishX float64
	solids          []solid
	portals         []portal
	gaps            []gap
	boosts, slows   []pad
	track           track
	camera          *cameraTable
	// Solids by 200-unit x bucket, flattened: bucket b holds
	// bucketSolids[bucketStart[b]:bucketStart[b+1]]. firstBucket is each
	// solid's first bucket, so a query spanning buckets visits it once.
	bucketStart  []int32
	bucketSolids []int32
	firstBucket  []int32
}

func newCourse(version int, length float64, solids []solid, portals []portal, gaps []gap, boosts, slows []pad, path track, camera *cameraTable) *course {
	built := &course{version: version, length: length, finishX: length - finishBehindLength, solids: solids, portals: portals,
		gaps: gaps, boosts: boosts, slows: slows, track: path, camera: camera}
	if len(boosts) > maximumPads || len(slows) > maximumPads {
		panic("faus switch: more pads than a pad set holds")
	}
	buckets := make([][]int32, int(length/courseBucketSize)+2)
	built.firstBucket = make([]int32, len(solids))
	for index, wall := range solids {
		first := max(0, int(wall.box.minX/courseBucketSize))
		last := min(len(buckets)-1, max(first, int(wall.box.maxX/courseBucketSize)))
		for bucket := first; bucket <= last; bucket++ {
			buckets[bucket] = append(buckets[bucket], int32(index))
		}
		built.firstBucket[index] = int32(first)
	}
	built.bucketStart = make([]int32, 0, len(buckets)+1)
	for _, bucket := range buckets {
		built.bucketStart = append(built.bucketStart, int32(len(built.bucketSolids)))
		built.bucketSolids = append(built.bucketSolids, bucket...)
	}
	built.bucketStart = append(built.bucketStart, int32(len(built.bucketSolids)))
	return built
}

// bucketRange is the buckets an area spans; first > last when it is outside
// the course.
func (path *course) bucketRange(area rect) (first, last int) {
	count := len(path.bucketStart) - 1
	first = max(0, int(area.minX/courseBucketSize))
	last = min(count-1, max(first, int(area.maxX/courseBucketSize)))
	return first, last
}

// visitSolidsNear calls visit for each solid that may touch area (a small
// superset), in the order FausSwitchCourse.solids(near:) visits them; contact
// resolution depends on that order.
func (path *course) visitSolidsNear(area rect, visit func(*solid)) {
	first, last := path.bucketRange(area)
	for bucket := first; bucket <= last; bucket++ {
		for _, index := range path.bucketSolids[path.bucketStart[bucket]:path.bucketStart[bucket+1]] {
			if max(int(path.firstBucket[index]), first) == bucket {
				visit(&path.solids[index])
			}
		}
	}
}

func (path *course) overlapsSolid(area rect) bool {
	first, last := path.bucketRange(area)
	for bucket := first; bucket <= last; bucket++ {
		for _, index := range path.bucketSolids[path.bucketStart[bucket]:path.bucketStart[bucket+1]] {
			if path.solids[index].box.intersects(area) {
				return true
			}
		}
	}
	return false
}

// adding is the same course with extra solids appended (ids continue).
func (path *course) adding(boxes []rect, kind solidKind) *course {
	solids := slices.Clip(slices.Clone(path.solids))
	for _, box := range boxes {
		solids = append(solids, solid{id: len(solids), kind: kind, box: box})
	}
	return newCourse(path.version, path.length, solids, path.portals, path.gaps, path.boosts, path.slows, path.track, path.camera)
}

// standardCourse is FausSwitchCourse.standard; raceCourses[n-1] adds the
// start platforms of a race of n robots (FausSwitchCourse.race(runnerCount:)).
var (
	standardCourse = buildStandardCourse()
	raceCourses    = buildRaceCourses()
)

// raceCourse is the course a race of runnerCount robots runs on.
func raceCourse(runnerCount int) *course { return raceCourses[clampRunnerCount(runnerCount)-1] }

func buildStandardCourse() *course {
	gaps := standardGaps[:]
	var solids []solid
	for _, rail := range []Gravity{GravityDown, GravityUp} {
		minY, maxY := ceilingSurface, worldHeight
		if rail == GravityDown {
			minY, maxY = 0, floorSurface
		}
		railGaps := []gap{}
		for _, hole := range gaps {
			if hole.rail == rail {
				railGaps = append(railGaps, hole)
			}
		}
		slices.SortStableFunc(railGaps, func(a, b gap) int {
			switch {
			case a.minX < b.minX:
				return -1
			case b.minX < a.minX:
				return 1
			}
			return 0
		})
		cursor := 0.0
		for _, hole := range railGaps {
			if hole.minX > cursor {
				solids = append(solids, solid{id: len(solids), kind: solidRail, box: rect{minX: cursor, minY: minY, maxX: hole.minX, maxY: maxY}})
			}
			cursor = swiftMax(cursor, hole.maxX)
		}
		solids = append(solids, solid{id: len(solids), kind: solidRail, box: rect{minX: cursor, minY: minY, maxX: standardLength, maxY: maxY}})
	}
	for _, block := range standardBlocks {
		centerX, width, minY, maxY := block[0], block[1], block[2], block[3]
		solids = append(solids, solid{id: len(solids), kind: solidBlock,
			box: rect{minX: centerX - width/2, minY: minY, maxX: centerX + width/2, maxY: maxY}})
	}
	slows := make([]pad, len(standardSlows))
	for index, source := range standardSlows {
		slows[index] = source.pad()
	}
	path := newTrack([]turn{
		{startX: turnStartX, radius: turnRadius, isLeft: true},
		{startX: secondTurnStartX, radius: turnRadius, isLeft: true},
	})
	camera := newCameraTable(standardLength, path, MaximumMatchTicks)
	return newCourse(standardCourseVersion, standardLength, solids, standardPortals[:], gaps, standardBoosts[:], slows, path, camera)
}

func buildRaceCourses() [MaximumPlayers]*course {
	var courses [MaximumPlayers]*course
	for count := 1; count <= MaximumPlayers; count++ {
		lanes := startLanes(count, runnerScale(count))
		platforms := make([]rect, len(lanes))
		for seat, lane := range lanes {
			platforms[seat] = lane.platform
		}
		courses[count-1] = standardCourse.adding(platforms, solidPlatform)
	}
	return courses
}

// turn is FausSwitchTrack.Turn: a quarter turn of the whole band.
type turn struct {
	startX, radius float64
	// isLeft turns counterclockwise.
	isLeft bool
}

// length is measured on the band's center line.
func (bend turn) length() float64 { return bend.radius * pi / 2 }
func (bend turn) endX() float64   { return bend.startX + bend.length() }

// track is the part of FausSwitchTrack the rules need: where the bends are
// and which straights run vertically. Bends are exact quarter turns, so the
// run directions are whole numbers and need no trigonometry.
type track struct {
	turns []turn
	// vertical[i]: straight i (before turn i, after turn i-1) runs vertically.
	vertical []bool
}

func newTrack(turns []turn) track {
	sorted := slices.Clone(turns)
	slices.SortStableFunc(sorted, func(a, b turn) int {
		switch {
		case a.startX < b.startX:
			return -1
		case b.startX < a.startX:
			return 1
		}
		return 0
	})
	directionX, directionY := 1, 0
	vertical := []bool{false}
	for _, bend := range sorted {
		if bend.isLeft {
			directionX, directionY = -directionY, directionX
		} else {
			directionX, directionY = directionY, -directionX
		}
		vertical = append(vertical, directionX == 0)
	}
	return track{turns: sorted, vertical: vertical}
}

// turnBlend is 0 on the straight before turn index, 1 after it, eased between.
func (path track) turnBlend(index int, x float64) float64 {
	bend := path.turns[index]
	if !(x > bend.startX) {
		return 0
	}
	if !(x < bend.endX()) {
		return 1
	}
	return smoothstep((x - bend.startX) / bend.length())
}

// smoothstep is 3x² − 2x³ on the clamped value.
func smoothstep(value float64) float64 {
	clamped := swiftMin(1, swiftMax(0, value))
	return clamped * clamped * (3 - float64(2*clamped))
}
