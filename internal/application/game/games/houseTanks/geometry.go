package houseTanks

import "math"

// Vector is an arena point or velocity: origin bottom-left, y up.
type Vector struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func vectorFromAngle(angle float64) Vector { return Vector{math.Cos(angle), math.Sin(angle)} }

func (vector Vector) add(other Vector) Vector { return Vector{vector.X + other.X, vector.Y + other.Y} }
func (vector Vector) sub(other Vector) Vector { return Vector{vector.X - other.X, vector.Y - other.Y} }
func (vector Vector) scale(factor float64) Vector {
	return Vector{vector.X * factor, vector.Y * factor}
}
func (vector Vector) dot(other Vector) float64 { return vector.X*other.X + vector.Y*other.Y }
func (vector Vector) length() float64          { return math.Hypot(vector.X, vector.Y) }
func (vector Vector) angle() float64           { return math.Atan2(vector.Y, vector.X) }
func (vector Vector) distance(other Vector) float64 {
	return vector.sub(other).length()
}

func (vector Vector) normalized() Vector {
	if length := vector.length(); length > 0 {
		return Vector{vector.X / length, vector.Y / length}
	}
	return Vector{}
}

// rotated turns the vector counter-clockwise by angle radians.
func (vector Vector) rotated(angle float64) Vector {
	c, s := math.Cos(angle), math.Sin(angle)
	return Vector{vector.X*c - vector.Y*s, vector.X*s + vector.Y*c}
}

// Rect is an axis-aligned box given by its bottom-left corner.
type Rect struct {
	X, Y, Width, Height float64
}

func (rect Rect) minX() float64 { return rect.X }
func (rect Rect) minY() float64 { return rect.Y }
func (rect Rect) maxX() float64 { return rect.X + rect.Width }
func (rect Rect) maxY() float64 { return rect.Y + rect.Height }

func (rect Rect) contains(point Vector) bool {
	return point.X >= rect.minX() && point.X <= rect.maxX() && point.Y >= rect.minY() && point.Y <= rect.maxY()
}

func (rect Rect) containsRect(other Rect) bool {
	return other.minX() >= rect.minX() && other.maxX() <= rect.maxX() && other.minY() >= rect.minY() && other.maxY() <= rect.maxY()
}

func (rect Rect) inset(dx, dy float64) Rect {
	return Rect{rect.X + dx, rect.Y + dy, rect.Width - dx*2, rect.Height - dy*2}
}

// ArenaRect is the whole logical arena.
func ArenaRect() Rect { return Rect{0, 0, ArenaWidth, ArenaHeight} }

// ArenaFrame is the playable frame inside the wall; shells and tanks stop here.
func ArenaFrame() Rect {
	inset := math.Max(MinimumWallInset, ArenaHeight*WallInsetRatio)
	return ArenaRect().inset(inset, inset)
}

// reflect bounces a velocity heading into a surface with the given normal.
func reflect(velocity, normal Vector, restitution float64) Vector {
	along := velocity.dot(normal)
	if along >= 0 {
		return velocity
	}
	return velocity.sub(normal.scale((1 + restitution) * along))
}

// segmentCircle is the fraction along start→end where it first enters the
// circle, 0 when it starts inside.
func segmentCircle(start, end, center Vector, radius float64) (float64, bool) {
	direction := end.sub(start)
	offset := start.sub(center)
	if offset.length() <= radius {
		return 0, true
	}
	a := direction.dot(direction)
	if a <= 0 {
		return 0, false
	}
	b := 2 * offset.dot(direction)
	c := offset.dot(offset) - radius*radius
	discriminant := b*b - 4*a*c
	if discriminant < 0 {
		return 0, false
	}
	fraction := (-b - math.Sqrt(discriminant)) / (2 * a)
	return fraction, fraction >= 0 && fraction <= 1
}

// segmentBox is the fraction where start→end first enters a rotated box,
// a slab test in box space.
func segmentBox(start, end, center Vector, halfWidth, halfHeight, rotation float64) (float64, bool) {
	a := start.sub(center).rotated(-rotation)
	b := end.sub(center).rotated(-rotation)
	entry, exit := 0.0, 1.0
	for _, axis := range [2][3]float64{{a.X, b.X - a.X, halfWidth}, {a.Y, b.Y - a.Y, halfHeight}} {
		origin, delta, extent := axis[0], axis[1], axis[2]
		if math.Abs(delta) < 1e-9 {
			if math.Abs(origin) > extent {
				return 0, false
			}
			continue
		}
		first, second := (-extent-origin)/delta, (extent-origin)/delta
		entry = math.Max(entry, math.Min(first, second))
		exit = math.Min(exit, math.Max(first, second))
		if entry > exit {
			return 0, false
		}
	}
	return entry, true
}

// exitFraction is where start→end leaves bounds; none while it stays inside.
func exitFraction(start, end Vector, bounds Rect) (float64, bool) {
	if bounds.contains(end) {
		return 0, false
	}
	if !bounds.contains(start) {
		return 0, true
	}
	direction := end.sub(start)
	fraction := 1.0
	if direction.X > 0 {
		fraction = math.Min(fraction, (bounds.maxX()-start.X)/direction.X)
	}
	if direction.X < 0 {
		fraction = math.Min(fraction, (bounds.minX()-start.X)/direction.X)
	}
	if direction.Y > 0 {
		fraction = math.Min(fraction, (bounds.maxY()-start.Y)/direction.Y)
	}
	if direction.Y < 0 {
		fraction = math.Min(fraction, (bounds.minY()-start.Y)/direction.Y)
	}
	return math.Max(0, fraction), true
}

// NormalizedAngle maps an angle into [-π, π].
func NormalizedAngle(angle float64) float64 {
	return math.Atan2(math.Sin(angle), math.Cos(angle))
}
