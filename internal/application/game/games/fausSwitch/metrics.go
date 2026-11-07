package fausSwitch

import "math"

// Engine metrics (FausSwitchMetrics and the course types' static values).
//
// They are variables, not constants, on purpose. Swift evaluates every derived
// value at run time on rounded float64 operands; Go folds constant expressions
// exactly at compile time. 33.6 * 0.75 is 25.200000000000003 at run time and
// 25.2 as a folded constant: a one-ulp difference that breaks bit-for-bit
// parity with the client. Keeping every operand a typed variable makes Go round
// at the same steps as Swift. Never write arithmetic on two literals in the
// engine; name the value here instead.
//
// The second rule is fused multiply-add. The Go spec lets the compiler turn
// x*y + z into one FMA instruction (it does on arm64), which rounds once where
// Swift rounds twice. Every product that is then added or subtracted in the
// engine is wrapped in an explicit float64(...) conversion, which the spec
// says rounds and forbids fusion: y += float64(velocityY * tickDuration).
// TestSwiftParityTrace catches a missing one on arm64.
var (
	// One correctly rounded division: folding it gives the same float64.
	tickDuration = 1.0 / PhysicsRateHz

	worldHeight = 390.0
	// viewportWidth is the camera's width in course units (a 390-high iPhone).
	viewportWidth = 844.0
	spawnX        = 380.0

	runnerWidth  = 27.2
	runnerHeight = 33.6

	railThickness  = 27.3
	floorSurface   = railThickness
	ceilingSurface = worldHeight - railThickness

	runSpeed        = 225.0
	gravityStrength = 2_900.0
	switchVelocity  = 270.0
	// flipCarry is the share of the vertical speed a flip keeps when it beats
	// switchVelocity.
	flipCarry       = 0.34
	boostExtraSpeed = 110.0
	slowPenalty     = 100.0
	// gapExitSpeed pushes a robot out through a rail gap.
	gapExitSpeed   = 312.0
	boundaryMargin = 80.0
	// finishReach: a robot finishes when its front is this close to finishX.
	finishReach = 8.0
	// finishBehindLength: finishX is this far before the course end.
	finishBehindLength = 120.0

	// Pads (boost and slow) are 48 × 28 strips.
	padWidth  = 48.0
	padHeight = 28.0

	portalHeight       = 40.0
	portalOpeningWidth = 8.0
	// portalTrailRoom is kept in front of the trail when an exit is behind it.
	portalTrailRoom = 24.0

	// A gap's sensor starts a full-size body width plus a margin inside each
	// edge; the robot scale never changes it.
	gapSensorInset        = runnerWidth + 10
	gapSensorRise         = 5.0
	gapSensorReach        = 18.0
	gapSensorMinimumWidth = 4.0

	platformThickness = 6.0
	platformBack      = 40.0
	platformAhead     = 120.0

	// The camera keeps horizontalBehind of the run behind its focus on
	// horizontal stretches and verticalBehind on the climb, zoomed out to
	// verticalZoom (FausSwitchCamera).
	horizontalBehind = spawnX
	verticalBehind   = 140.0
	verticalZoom     = 0.75
	horizontalAhead  = viewportWidth - horizontalBehind
	verticalAhead    = worldHeight/verticalZoom - verticalBehind

	// pi is Double.pi: rounded once, like Swift, before it is multiplied.
	pi = math.Pi

	maximumSupportInset = 7.0
	supportInsetShare   = 0.2
	supportProbeReach   = 0.5
	contactEpsilon      = 1e-9
)

const (
	boostTicks       int64 = 78 // 0.65 s
	slowTicks        int64 = 96 // 0.8 s
	contactPasses          = 4
	courseBucketSize       = 200.0
)

// swiftMin and swiftMax are Swift.min and Swift.max for two Doubles. Go's
// builtins differ for signed zeros (and NaN), which would break bit parity.
func swiftMin(x, y float64) float64 {
	if y < x {
		return y
	}
	return x
}

func swiftMax(x, y float64) float64 {
	if y >= x {
		return y
	}
	return x
}

func (gravity Gravity) sign() float64 {
	if gravity == GravityDown {
		return -1
	}
	return 1
}

func (gravity Gravity) opposite() Gravity {
	if gravity == GravityDown {
		return GravityUp
	}
	return GravityDown
}

// seconds is FausSwitchMetrics.seconds.
func seconds(ticks int64) float64 { return float64(ticks) * tickDuration }
