// Package fausSwitch is Faus Switch online: a server-authoritative race of two
// to eight robots that flip gravity between the floor and ceiling rails. Its
// rules port the Swift reference engine in FausCrewCore (courseVersion 7) bit
// for bit; fixtures/fausSwitchProtocol.json is the contract the client wrote.
//
// The package is layered; each layer depends only on the ones below it:
//
//	contract    definition, protocol, models, settings, colors, result*
//	session     module, runtime, wireSnapshot, runtimeCheckpoint, recoveryResult
//	simulation  simulation* (ticks, rewind ring, input log, race end window)
//	engine      metrics, course*, camera, startGrid, physics (pure, no time or JSON)
package fausSwitch

import (
	"errors"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
)

const (
	GameKey          = "fausSwitch"
	ProtocolVersion  = 2
	ContractRevision = "fausSwitch.v1"
	// CourseVersion is checked by the client in the welcome, every snapshot
	// and the result; any other value is refused.
	CourseVersion  = 7
	MinimumPlayers = 2
	MaximumPlayers = 8
	// Lobby rules shared with the core.
	ReadyWindowDuration = 30 * time.Second
	CountdownDuration   = 3 * time.Second
	// Presence and network defaults; the operator may tune them (Settings).
	ControlHeartbeatInterval = 2 * time.Second
	ControlTimeout           = 6 * time.Second
	ReconnectGraceDuration   = 10 * time.Second
	SnapshotRateHz           = 20
	MaximumInputRateHz       = 20
	// Contract timing: the simulation and the client depend on these.
	PhysicsRateHz    = 120
	SchedulingRateHz = 60
	// A flip is applied at the tick the player saw, at most this far back.
	MaximumRewindDuration = 250 * time.Millisecond
	MaximumRewindTicks    = 30
	MaximumMatchDuration  = 120 * time.Second
	MaximumMatchTicks     = 14_400
	// A race that starts with more than CrowdedRunnerThreshold robots runs
	// every robot at CrowdedRunnerScale of its size (briefing §4.2.2).
	CrowdedRunnerThreshold = 4
	CrowdedRunnerScale     = 0.75
)

type Phase = module.Phase

const (
	PhaseCountdown  = module.PhaseCountdown
	PhasePlaying    = module.PhasePlaying
	PhaseRecovering = module.PhaseRecovering
	PhaseFinalizing = module.PhaseFinalizing
	PhaseEnded      = module.PhaseEnded
	PhaseCancelled  = module.PhaseCancelled
)

func Definition() gameDomain.GameDefinition {
	return gameDomain.GameDefinition{
		GameKey:         GameKey,
		ProtocolVersion: ProtocolVersion,
		Mode:            gameDomain.RealtimeGame,
		Rules: gameDomain.SessionRules{
			MinimumPlayers:      MinimumPlayers,
			MaximumPlayers:      MaximumPlayers,
			ReadyWindowDuration: ReadyWindowDuration,
			CountdownDuration:   CountdownDuration,
		},
	}
}

// Color is a robot's color; every robot of a race has its own.
type Color string

const (
	ColorPlum     Color = "plum"
	ColorForest   Color = "forest"
	ColorOcean    Color = "ocean"
	ColorLavender Color = "lavender"
	ColorOlive    Color = "olive"
	ColorBronze   Color = "bronze"
	ColorGraphite Color = "graphite"
	ColorBerry    Color = "berry"
)

// PlayerColors is the catalog order: a player without a valid claim gets the
// first color nobody holds, in this order.
func PlayerColors() []Color {
	return []Color{ColorPlum, ColorForest, ColorOcean, ColorLavender, ColorOlive, ColorBronze, ColorGraphite, ColorBerry}
}

func (color Color) Valid() bool {
	switch color {
	case ColorPlum, ColorForest, ColorOcean, ColorLavender, ColorOlive, ColorBronze, ColorGraphite, ColorBerry:
		return true
	}
	return false
}

// Gravity, RunnerStatus and EliminationReason are one byte each so a robot's
// state stays small and pointer-free (the rewind ring copies it every tick);
// on the wire they are their names. The zero value is invalid on the wire.

// Gravity is the rail that pulls a robot: down toward the floor, up toward
// the ceiling.
type Gravity int8

const (
	GravityDown Gravity = iota + 1
	GravityUp
)

var gravityNames = enumNames[Gravity]{GravityDown: "down", GravityUp: "up"}

func (gravity Gravity) MarshalText() ([]byte, error) { return gravityNames.marshal(gravity) }
func (gravity *Gravity) UnmarshalText(text []byte) error {
	return gravityNames.unmarshal(gravity, text)
}
func (gravity Gravity) String() string { return gravityNames[gravity] }

type RunnerStatus int8

const (
	RunnerRunning RunnerStatus = iota + 1
	RunnerFinished
	RunnerCrashed
)

var runnerStatusNames = enumNames[RunnerStatus]{RunnerRunning: "running", RunnerFinished: "finished", RunnerCrashed: "crashed"}

func (status RunnerStatus) MarshalText() ([]byte, error) { return runnerStatusNames.marshal(status) }
func (status *RunnerStatus) UnmarshalText(text []byte) error {
	return runnerStatusNames.unmarshal(status, text)
}
func (status RunnerStatus) String() string { return runnerStatusNames[status] }

// EliminationReason is why a robot left the race; laser was removed in
// course 6 and the client no longer decodes it. The zero value is "none".
type EliminationReason int8

const (
	EliminationBoundary EliminationReason = iota + 1
	EliminationLeftBehind
	EliminationForfeit
	EliminationMembershipRevoked
)

var eliminationReasonNames = enumNames[EliminationReason]{EliminationBoundary: "boundary", EliminationLeftBehind: "leftBehind",
	EliminationForfeit: "forfeit", EliminationMembershipRevoked: "membershipRevoked"}

func (reason EliminationReason) MarshalText() ([]byte, error) {
	return eliminationReasonNames.marshal(reason)
}
func (reason *EliminationReason) UnmarshalText(text []byte) error {
	return eliminationReasonNames.unmarshal(reason, text)
}
func (reason EliminationReason) String() string { return eliminationReasonNames[reason] }

var ErrInvalidEnum = errors.New("faus switch: unknown enum value")

// enumNames maps a one-byte enum to its wire name.
type enumNames[T ~int8] map[T]string

func (names enumNames[T]) marshal(value T) ([]byte, error) {
	name, ok := names[value]
	if !ok {
		return nil, ErrInvalidEnum
	}
	return []byte(name), nil
}

func (names enumNames[T]) unmarshal(target *T, text []byte) error {
	for value, name := range names {
		if name == string(text) {
			*target = value
			return nil
		}
	}
	return ErrInvalidEnum
}

type ResultStatus string

const (
	ResultCompleted ResultStatus = "completed"
	ResultCancelled ResultStatus = "cancelled"
)

type EndReason string

const (
	// EndRaceComplete: no robot is running any more.
	EndRaceComplete EndReason = "raceComplete"
	// EndTimeLimit: MaximumMatchTicks passed with robots still running.
	EndTimeLimit               EndReason = "timeLimit"
	EndInsufficientPlayers     EndReason = "insufficientPlayers"
	EndCancelledByUser         EndReason = module.EndCancelledByUser
	EndSessionExpired          EndReason = "sessionExpired"
	EndRecoveryFailed          EndReason = module.EndRecoveryFailed
	EndCoordinationUnavailable EndReason = "coordinationUnavailable"
	EndRuntimeOverloaded       EndReason = "runtimeOverloaded"
)

// Completes reports whether a race that ended this way has a completed result.
func (reason EndReason) Completes() bool {
	return reason == EndRaceComplete || reason == EndTimeLimit
}

// Cancels reports whether a race that ended this way is cancelled; the rest
// of the core's end reasons are not Faus Switch results.
func (reason EndReason) Cancels() bool {
	switch reason {
	case EndInsufficientPlayers, EndCancelledByUser, EndSessionExpired, EndRecoveryFailed, EndCoordinationUnavailable, EndRuntimeOverloaded:
		return true
	}
	return false
}
