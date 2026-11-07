package fausSwitch

import (
	"cmp"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// The standard course matches briefing §4.2 and FausSwitchCourse.standard.
func TestStandardCourseLayout(t *testing.T) {
	path := standardCourse
	if path.version != CourseVersion || path.length != 20_400 || path.finishX != 20_280 {
		t.Fatalf("version %d length %v finish %v", path.version, path.length, path.finishX)
	}
	type gapSpec struct {
		minX, maxX float64
		rail       Gravity
	}
	gaps := []gapSpec{{3820, 4010, GravityDown}, {4580, 4770, GravityUp}, {6520, 6710, GravityDown}, {7800, 7990, GravityUp},
		{12000, 12190, GravityDown}, {13420, 13610, GravityUp}, {17480, 17670, GravityDown}, {19040, 19230, GravityUp}}
	for index, hole := range path.gaps {
		if hole.id != index || (gapSpec{hole.minX, hole.maxX, hole.rail}) != gaps[index] {
			t.Fatalf("gap %d = %+v", index, hole)
		}
	}
	portals := []portal{{0, 2190, GravityDown, 1970}, {1, 3330, GravityUp, 3110}, {2, 5150, GravityDown, 4930}, {3, 7260, GravityUp, 7040},
		{4, 11450, GravityDown, 11350}, {5, 14200, GravityUp, 14100}, {6, 18220, GravityDown, 18000}, {7, 19830, GravityUp, 19610}}
	if !slices.Equal(path.portals, portals) || len(path.gaps) != len(gaps) {
		t.Fatalf("portals = %+v", path.portals)
	}
	boosts := []pad{{0, 1660, 150}, {1, 2720, 230}, {2, 5170, 190}, {3, 6300, 140}, {4, 8520, 250}, {5, 12900, 200},
		{6, 14850, 150}, {7, 16700, 200}, {8, 18670, 230}}
	if !slices.Equal(path.boosts, boosts) {
		t.Fatalf("boosts = %+v", path.boosts)
	}
	// Slow pads on a rail lie on its surface (y = 41.3 floor, 348.7 ceiling).
	floor, ceiling := floorSurface+padHeight/2, ceilingSurface-padHeight/2
	slows := []pad{{0, 1200, floor}, {1, 2330, ceiling}, {2, 4100, floor}, {3, 4880, ceiling}, {4, 6170, 205}, {5, 7380, floor},
		{6, 8080, ceiling}, {7, 12270, floor}, {8, 13310, 195}, {9, 13690, ceiling}, {10, 14320, floor}, {11, 17750, floor},
		{12, 19310, ceiling}, {13, 19700, 200}}
	if !slices.Equal(path.slows, slows) || math.Abs(floor-41.3) > 1e-12 || math.Abs(ceiling-348.7) > 1e-12 {
		t.Fatalf("slows = %+v", path.slows)
	}
	// Ten rail segments (each rail split by its four gaps), then 75 blocks;
	// ids are positions.
	rails, blocks := 0, 0
	for index, wall := range path.solids {
		if wall.id != index {
			t.Fatalf("solid %d has id %d", index, wall.id)
		}
		switch wall.kind {
		case solidRail:
			rails++
			if blocks > 0 {
				t.Fatal("rails come before blocks")
			}
		case solidBlock:
			blocks++
		default:
			t.Fatalf("standard course has a %v solid", wall.kind)
		}
	}
	if rails != 10 || blocks != 75 {
		t.Fatalf("%d rails, %d blocks", rails, blocks)
	}
	if path.solids[0].box != (rect{0, 0, 3820, floorSurface}) || path.solids[9].box != (rect{19230, ceilingSurface, 20_400, worldHeight}) {
		t.Fatalf("rail ends = %+v %+v", path.solids[0].box, path.solids[9].box)
	}
	if block := path.solids[10].box; block != (rect{969, 0, 1031, 150}) {
		t.Fatalf("first block = %+v", block)
	}
	if ceilingSurface != 362.7 || floorSurface != 27.3 {
		t.Fatalf("rail surfaces %v %v", floorSurface, ceilingSurface)
	}
}

func TestCourseBendsAreClearAndEverySectionHasObstacles(t *testing.T) {
	path := standardCourse
	turns := path.track.turns
	if len(turns) != 2 || !turns[0].isLeft || !turns[1].isLeft || turns[0].startX != 9_100 || turns[1].startX != 15_450 ||
		math.Abs(turns[0].endX()-9_720.46) > 0.01 || !slices.Equal(path.track.vertical, []bool{false, true, false}) {
		t.Fatalf("track = %+v", path.track)
	}
	for _, bend := range turns {
		inBend := rect{minX: bend.startX, minY: floorSurface + 0.01, maxX: bend.endX(), maxY: ceilingSurface - 0.01}
		if path.overlapsSolid(inBend) {
			t.Fatal("no blocks in a bend")
		}
		near := func(minX, maxX float64) bool { return maxX > bend.startX-50 && minX < bend.endX()+50 }
		for _, door := range path.portals {
			if near(door.exitX, door.x) {
				t.Fatalf("portal %d near a bend", door.id)
			}
		}
		for _, hole := range path.gaps {
			if near(hole.minX, hole.maxX) {
				t.Fatalf("gap %d near a bend", hole.id)
			}
		}
		for _, strip := range append(slices.Clone(path.slows), path.boosts...) {
			if near(strip.x, strip.x) {
				t.Fatalf("pad at %v near a bend", strip.x)
			}
		}
	}
	for _, section := range [][2]float64{{turns[0].endX(), turns[1].startX}, {turns[1].endX(), path.finishX}} {
		blocks := 0
		for _, wall := range path.solids {
			if wall.kind == solidBlock && wall.box.minX > section[0] && wall.box.maxX < section[1] {
				blocks++
			}
		}
		if blocks < 15 {
			t.Fatalf("section %v has %d blocks", section, blocks)
		}
	}
	if !(path.finishX > turns[1].endX()+4_000) {
		t.Fatal("the leftward run is long enough")
	}
}

func TestCourseGroupsAndPadsSitClear(t *testing.T) {
	path := standardCourse
	var blocks []solid
	for _, wall := range path.solids {
		if wall.kind == solidBlock {
			blocks = append(blocks, wall)
		}
	}
	slices.SortStableFunc(blocks, func(a, b solid) int { return cmp.Compare(a.box.minX, b.box.minX) })
	var groups [][]solid
	for _, block := range blocks {
		if len(groups) > 0 && block.box.minX-groups[len(groups)-1][len(groups[len(groups)-1])-1].box.maxX < 200 {
			groups[len(groups)-1] = append(groups[len(groups)-1], block)
		} else {
			groups = append(groups, []solid{block})
		}
	}
	for _, group := range groups {
		if len(group) < 2 || len(group) > 3 {
			t.Fatalf("group at %v has %d blocks", group[0].box.minX, len(group))
		}
	}
	for _, strip := range path.boosts {
		box := strip.box()
		if !(box.minY > floorSurface+runnerHeight) || !(box.maxY < ceilingSurface-runnerHeight) || path.overlapsSolid(box) {
			t.Fatalf("boost %d is within reach of a rail", strip.id)
		}
	}
	for _, strip := range path.slows {
		inner := strip.box()
		inner.minY += 0.01
		inner.maxY -= 0.01
		if path.overlapsSolid(inner) {
			t.Fatalf("slow pad %d overlaps a solid", strip.id)
		}
		for _, boost := range path.boosts {
			if boost.box().intersects(strip.box()) {
				t.Fatalf("slow pad %d overlaps a boost", strip.id)
			}
		}
		for _, hole := range path.gaps {
			if hole.maxX > strip.box().minX && hole.minX < strip.box().maxX {
				t.Fatalf("slow pad %d over a gap", strip.id)
			}
		}
	}
}

func TestPortalsAreJustBigEnoughAndLandOnClearRail(t *testing.T) {
	path := standardCourse
	if !(portalHeight > runnerHeight) || !(portalHeight < runnerHeight*1.3) {
		t.Fatal("portal height")
	}
	for _, door := range path.portals {
		if !(door.exitX < door.x) || path.overlapsSolid(door.opening()) {
			t.Fatalf("portal %d", door.id)
		}
		exit := centeredRect(door.exitX, door.exitY(), runnerWidth, runnerHeight)
		if path.overlapsSolid(exit) {
			t.Fatalf("portal %d exit blocked", door.id)
		}
		for _, other := range path.portals {
			if other.opening().intersects(exit) {
				t.Fatalf("portal %d exits into a portal", door.id)
			}
		}
		stretch := rect{minX: door.exitX - runnerWidth/2, minY: exit.minY + 0.01, maxX: door.x, maxY: exit.maxY - 0.01}
		if path.overlapsSolid(stretch) {
			t.Fatalf("portal %d landing stretch blocked", door.id)
		}
		for _, hole := range path.gaps {
			if hole.sensor().intersects(exit) || (hole.rail == door.exitGravity() && hole.maxX > stretch.minX-40 && hole.minX < stretch.maxX+40) {
				t.Fatalf("portal %d lands near a gap", door.id)
			}
		}
		for _, strip := range path.slows {
			if strip.box().intersects(stretch) {
				t.Fatalf("portal %d lands on a slow pad", door.id)
			}
		}
		climbing := door.x > path.track.turns[0].endX() && door.x < path.track.turns[1].startX
		throw := 220.0
		if climbing {
			throw = 100
		}
		if door.x-door.exitX < throw {
			t.Fatalf("portal %d throws back %v", door.id, door.x-door.exitX)
		}
	}
}

// visitSolidsNear visits every solid touching the area exactly once, in the
// order of FausSwitchCourse.solids(near:): bucket by bucket, each solid in the
// first queried bucket it sits in.
func TestSolidsNearIsAnOrderedSuperset(t *testing.T) {
	random := rand.New(rand.NewPCG(7, 11))
	for _, path := range append([]*course{standardCourse}, raceCourses[:]...) {
		for range 2_000 {
			minX := random.Float64()*21_000 - 300
			minY := random.Float64() * 390
			area := rect{minX: minX, minY: minY, maxX: minX + random.Float64()*700, maxY: minY + random.Float64()*120}
			first, _ := path.bucketRange(area)
			var visited []int
			lastBucket := -1
			path.visitSolidsNear(area, func(wall *solid) {
				bucket := max(int(path.firstBucket[wall.id]), first)
				if bucket < lastBucket {
					t.Fatal("buckets visited out of order")
				}
				lastBucket = bucket
				visited = append(visited, wall.id)
			})
			sorted := slices.Clone(visited)
			slices.Sort(sorted)
			if len(slices.Compact(sorted)) != len(visited) {
				t.Fatal("a solid was visited twice")
			}
			overlaps := false
			for _, wall := range path.solids {
				if wall.box.intersects(area) {
					overlaps = true
					if !slices.Contains(visited, wall.id) {
						t.Fatalf("solid %d touches %+v but was not visited", wall.id, area)
					}
				}
			}
			if overlaps != path.overlapsSolid(area) {
				t.Fatal("overlapsSolid disagrees")
			}
		}
	}
}

// FausSwitchCollisionTests.testEveryRobotStartsOnItsOwnPlatform and the
// briefing's control tables (§4.2, §4.2.2).
func TestEveryRobotStartsOnItsOwnPlatform(t *testing.T) {
	for count := 1; count <= MaximumPlayers; count++ {
		scale := runnerScale(count)
		if (count > 4) != (scale == 0.75) || (count <= 4) != (scale == 1) {
			t.Fatalf("scale %v for %d", scale, count)
		}
		lanes := startLanes(count, scale)
		path := raceCourse(count)
		rules := newRacePhysics(count)
		var platforms []rect
		for index, wall := range path.solids {
			if index < len(standardCourse.solids) && wall != standardCourse.solids[index] {
				t.Fatal("a race course starts with the standard solids")
			}
			if wall.kind == solidPlatform {
				platforms = append(platforms, wall.box)
			}
		}
		if len(platforms) != count || path.version != CourseVersion || path.camera != standardCourse.camera ||
			!slices.Equal(path.track.vertical, standardCourse.track.vertical) {
			t.Fatalf("race course of %d", count)
		}
		states := make([]runner, count)
		for seat, start := range lanes {
			states[seat] = start.spawn
			gravity := GravityDown
			if seat%2 == 1 {
				gravity = GravityUp
			}
			if platforms[seat] != start.platform || start.spawn.x != spawnX || start.gravity != gravity || start.spawn.gravity != gravity {
				t.Fatalf("seat %d of %d = %+v", seat, count, start)
			}
			if !start.platform.intersects(rules.size.supportProbe(&start.spawn)) {
				t.Fatalf("seat %d of %d does not stand on its platform", seat, count)
			}
			if !(start.platform.minY > floorSurface) || !(start.platform.maxY < ceilingSurface) {
				t.Fatalf("seat %d of %d platform leaves the band", seat, count)
			}
			body := rules.size.body(&start.spawn)
			path.visitSolidsNear(body, func(wall *solid) {
				if overlapDepth(wall.box, body) >= 1e-9 {
					t.Fatalf("seat %d of %d starts inside solid %d", seat, count, wall.id)
				}
			})
		}
		assertNoRobotOverlap(t, &rules, states, -1)
	}
	eight := startLanes(8, 1)
	if !(eight[1].spawn.y > eight[7].spawn.y) {
		t.Fatal("seat 1 is the highest lane")
	}
}

func TestStartGridMatchesTheBackendTables(t *testing.T) {
	spawnsOf := func(lanes []lane) (minYs, spawns []float64) {
		for _, start := range lanes {
			minYs = append(minYs, start.platform.minY)
			spawns = append(spawns, start.spawn.y)
		}
		return minYs, spawns
	}
	cases := []struct {
		count         int
		scale         float64
		minYs, spawns []float64
	}{
		{2, 1, []float64{91.3, 292.6}, []float64{114.1, 275.8}},
		{5, 0.75, []float64{45.2, 338.7, 112.3, 271.6, 179.4}, []float64{63.8, 326.1, 130.9, 259.0, 198.0}},
		{8, 0.75, []float64{32.6, 351.3, 74.5, 309.4, 116.5, 267.4, 158.4, 225.5},
			[]float64{51.2, 338.7, 93.1, 296.8, 135.1, 254.8, 177.0, 212.9}},
	}
	for _, expected := range cases {
		minYs, spawns := spawnsOf(startLanes(expected.count, expected.scale))
		if !slices.Equal(minYs, expected.minYs) || !slices.Equal(spawns, expected.spawns) {
			t.Fatalf("n = %d: platforms %v spawns %v", expected.count, minYs, spawns)
		}
		for _, start := range startLanes(expected.count, expected.scale) {
			if start.platform.maxY-start.platform.minY < 5.99 || start.platform.minX != 340 || start.platform.maxX != 500 {
				t.Fatalf("platform %+v", start.platform)
			}
		}
	}
	// The run-time product Swift uses, not the folded constant (25.2).
	size := newRunnerSize(CrowdedRunnerScale)
	if size.height != 25.200000000000003 || size.width != runnerWidth*CrowdedRunnerScale || size.probeInset != size.width*0.2 {
		t.Fatalf("crowded size = %+v", size)
	}
	if full := newRunnerSize(1); full.width != 27.2 || full.height != 33.6 || full.probeInset != 5.44 {
		t.Fatalf("full size = %+v", full)
	}
}

func overlapDepth(a, b rect) float64 { return a.depth(b) }
