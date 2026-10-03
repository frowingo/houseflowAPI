package houseRockets

import (
	"math"
	"strconv"
)

const courseLookahead = 620.0
const courseTrailingMargin = 100.0

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Polygon []Point

type PassageSection struct {
	OffsetX float64 `json:"offsetX"`
	LowerY  float64 `json:"lowerY"`
	UpperY  float64 `json:"upperY"`
}

type Gate struct {
	ID       string           `json:"id"`
	WorldX   float64          `json:"worldX"`
	Sections []PassageSection `json:"sections"`
}

type SpeedField struct {
	ID            string      `json:"id"`
	WorldX        float64     `json:"worldX"`
	Effect        SpeedEffect `json:"effect"`
	Phase         float64     `json:"phase"`
	PeriodSeconds float64     `json:"periodSeconds"`
}

type CourseOrientation struct {
	Angle            float64
	IsTransitioning  bool
	TargetIsVertical *bool
}

func GateAt(index int) (Gate, error) {
	if index < 0 || index >= maximumCourseEntries {
		return Gate{}, ErrInvalidInput
	}
	var profile [][3]float64
	if index == 0 {
		profile = [][3]float64{{-46, 180, 190}, {46, 180, 190}}
	} else {
		switch (index - 1) % 6 {
		case 0:
			profile = [][3]float64{{-160, 180, 290}, {160, 120, 88}}
		case 1:
			profile = [][3]float64{{-145, 245, 90}, {145, 180, 290}}
		case 2:
			profile = [][3]float64{{-170, 100, 108}, {170, 255, 108}}
		case 3:
			profile = [][3]float64{{-150, 180, 280}, {0, 195, 82}, {150, 180, 280}}
		case 4:
			profile = [][3]float64{{-140, 255, 120}, {140, 105, 100}}
		default:
			profile = [][3]float64{{-38, 120, 88}, {38, 120, 88}}
		}
	}
	gate := Gate{ID: "gate:" + strconv.Itoa(index), WorldX: FirstGateX + float64(index)*GateSpacing, Sections: make([]PassageSection, len(profile))}
	for i, section := range profile {
		gate.Sections[i] = PassageSection{OffsetX: section[0], LowerY: section[1] - section[2]/2, UpperY: section[1] + section[2]/2}
	}
	return gate, nil
}

func SpeedFieldAt(index int) (SpeedField, error) {
	if index < 0 || index >= maximumCourseEntries {
		return SpeedField{}, ErrInvalidInput
	}
	effect, period := EffectBoost, BoostPeriodSeconds
	if index%2 != 0 {
		effect, period = EffectSlow, SlowPeriodSeconds
	}
	return SpeedField{ID: "field:" + strconv.Itoa(index), WorldX: FirstGateX + float64(index)*GateSpacing + GateSpacing/2,
		Effect: effect, Phase: float64(index) * 1.7, PeriodSeconds: period}, nil
}

func (field SpeedField) WorldYAt(elapsedSeconds float64) float64 {
	return 180 + math.Sin(elapsedSeconds*2*math.Pi/field.PeriodSeconds+field.Phase)*112
}

func (gate Gate) maxX() float64 { return gate.WorldX + gate.Sections[len(gate.Sections)-1].OffsetX }

func (gate Gate) SolidPolygons() []Polygon {
	if len(gate.Sections) < 2 {
		return []Polygon{}
	}
	polygons := make([]Polygon, 0, 2*(len(gate.Sections)-1))
	for index := 0; index+1 < len(gate.Sections); index++ {
		left, right := gate.Sections[index], gate.Sections[index+1]
		a, b := gate.WorldX+left.OffsetX, gate.WorldX+right.OffsetX
		polygons = append(polygons,
			Polygon{{a, 0}, {b, 0}, {b, right.LowerY}, {a, left.LowerY}},
			Polygon{{a, left.UpperY}, {b, right.UpperY}, {b, TrackHeight}, {a, TrackHeight}})
	}
	return polygons
}

func CourseOrientationAt(elapsedSeconds float64) (CourseOrientation, error) {
	if !finite(elapsedSeconds) || elapsedSeconds < 0 {
		return CourseOrientation{}, ErrInvalidInput
	}
	var orientation CourseOrientation
	cycleTime := math.Mod(elapsedSeconds, TransitionIntervalSeconds*2)
	vertical := cycleTime >= TransitionIntervalSeconds
	if elapsedSeconds >= TransitionIntervalSeconds {
		elapsed := cycleTime
		if vertical {
			elapsed -= TransitionIntervalSeconds
		}
		t := math.Min(1, elapsed/TransitionDurationSeconds)
		progress := t * t * (3 - 2*t)
		if !vertical {
			progress = 1 - progress
		}
		orientation.Angle = progress * math.Pi / 2
	}
	phase := math.Mod(elapsedSeconds, TransitionIntervalSeconds)
	orientation.IsTransitioning = elapsedSeconds >= TransitionIntervalSeconds && phase < TransitionDurationSeconds
	if orientation.IsTransitioning {
		orientation.TargetIsVertical = &vertical
	} else if phase >= TransitionIntervalSeconds-TransitionDurationSeconds {
		target := !vertical
		orientation.TargetIsVertical = &target
	}
	return orientation, nil
}

func IsBehindCamera(centerX, cameraX float64) bool {
	return finite(centerX) && finite(cameraX) && centerX+RocketRadius < cameraX
}

func ResolveContact(point Point, radius float64, polygon Polygon) (Point, error) {
	if !finite(point.X) || !finite(point.Y) || !finite(radius) || radius <= 0 || !finite(radius*radius) || len(polygon) < 3 || len(polygon) > 16 {
		return Point{}, ErrInvalidInput
	}
	hasTurn := false
	for index, a := range polygon {
		b, c := polygon[(index+1)%len(polygon)], polygon[(index+2)%len(polygon)]
		if !finite(a.X) || !finite(a.Y) {
			return Point{}, ErrInvalidInput
		}
		cross := (b.X-a.X)*(c.Y-b.Y) - (b.Y-a.Y)*(c.X-b.X)
		lengthSquared := (b.X-a.X)*(b.X-a.X) + (b.Y-a.Y)*(b.Y-a.Y)
		if !finite(cross) || cross < 0 || !finite(lengthSquared) || lengthSquared == 0 {
			return Point{}, ErrInvalidInput
		}
		hasTurn = hasTurn || cross > 0
	}
	if !hasTurn {
		return Point{}, ErrInvalidInput
	}
	result := resolveContact(point, radius, polygon)
	if !finite(result.X) || !finite(result.Y) {
		return Point{}, ErrInvalidInput
	}
	return result, nil
}

// The simulation only uses prevalidated convex, counter-clockwise course polygons.
func resolveContact(point Point, radius float64, polygon Polygon) Point {
	inside, closest, minimumDistanceSquared := true, point, math.Inf(1)
	var outward Point
	for index, a := range polygon {
		b := polygon[(index+1)%len(polygon)]
		dx, dy := b.X-a.X, b.Y-a.Y
		lengthSquared := dx*dx + dy*dy
		if lengthSquared == 0 {
			continue
		}
		if dx*(point.Y-a.Y)-dy*(point.X-a.X) < 0 {
			inside = false
		}
		t := math.Max(0, math.Min(1, ((point.X-a.X)*dx+(point.Y-a.Y)*dy)/lengthSquared))
		candidate := Point{a.X + t*dx, a.Y + t*dy}
		distanceSquared := (point.X-candidate.X)*(point.X-candidate.X) + (point.Y-candidate.Y)*(point.Y-candidate.Y)
		if distanceSquared < minimumDistanceSquared {
			minimumDistanceSquared, closest = distanceSquared, candidate
			length := math.Sqrt(lengthSquared)
			outward = Point{dy / length, -dx / length}
		}
	}
	if !inside && minimumDistanceSquared >= radius*radius {
		return point
	}
	distance := math.Sqrt(minimumDistanceSquared)
	if !inside && distance > 0.000001 {
		outward = Point{(point.X - closest.X) / distance, (point.Y - closest.Y) / distance}
	}
	return Point{closest.X + outward.X*radius, closest.Y + outward.Y*radius}
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
