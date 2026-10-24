// Package houseTanks is FauseTanks online: a server-authoritative arena match
// played in rounds until one player has won three. Its rules port the Swift
// reference engine in FausCrewCore; the fixtures in this package are the
// contract both clients follow.
package houseTanks

import (
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
)

const (
	GameKey           = "houseTanks"
	ProtocolVersion   = 2
	ContractRevision  = "houseTanks.v1"
	MapCatalogVersion = 1
	MinimumPlayers    = 2
	MaximumPlayers    = 8
	// Lobby rules shared with the core.
	ReadyWindowDuration = 30 * time.Second
	CountdownDuration   = 3 * time.Second
	// Presence and network defaults; the operator may tune them.
	ControlHeartbeatInterval      = 2 * time.Second
	ControlTimeout                = 6 * time.Second
	ReconnectGraceDuration        = 10 * time.Second
	PhysicsRateHz                 = 120
	SchedulingRateHz              = 60
	SnapshotRateHz                = 20
	MaximumInputRateHz            = 20
	MaximumPressMessagesPerSecond = 30
	LagCompensationWindow         = 150 * time.Millisecond
	HeadingToleranceMilliradians  = 35
	// Match rules. Rounds have no clock; the match has a safety limit, and a
	// round-ready phase in which a ready window never opens ends it as well.
	RoundWinsNeeded          = 3
	ArmorPerRound            = 5
	RoundCountdownDuration   = 3 * time.Second
	RoundReadyWindowDuration = 30 * time.Second
	RoundReadyIdleLimit      = 2 * time.Minute
	MaximumMatchDuration     = 5 * time.Minute
	// EventWindow is how long an event stays in every snapshot, so a frame
	// dropped for a slow connection cannot lose it.
	EventWindow = time.Second
)

// Arena and physics contract, in arena points (origin bottom-left, y up),
// radians counter-clockwise from +x, and seconds of 1/120 s ticks.
const (
	ArenaWidth        = 1180.0
	ArenaHeight       = 640.0
	MinimumWallInset  = 16.0
	WallInsetRatio    = 0.035
	GameplayScale     = 0.5
	ObstacleBaseScale = 0.72
	TankRadius        = 9.0
	TankLength        = 20.0
	RotationSpeed     = 1.5
	DriveForce        = 360.0
	TankMass          = 1.25
	TankDamping       = 1.8
	MaximumTankSpeed  = 245.0
	// TankRestitution applies against the arena frame and between live tanks,
	// ObstacleRestitution against fixed cover and wrecks.
	TankRestitution     = 0.36
	ObstacleRestitution = 0.2
	CrateRestitution    = 0.05
	CrateMass           = 0.45
	CrateDamping        = 4.0
	FireCooldown        = 0.58
	HitProtection       = 0.36
	ShellSpeed          = 510.0
	ShellRadius         = 1.8
	ShellLifetime       = 2.8
	ShellMuzzleOffset   = 15.0
	RocketSpeed         = 400.0
	RocketRadius        = 4.0
	RocketLength        = 22.0
	RocketLifetime      = 3.2
	RocketMuzzleOffset  = 25.0
	SpawnClearance      = 28.0
	ContactIterations   = 3
)

// Joker contract.
const (
	PowerUpDuration           = 8.0
	PowerUpTokenLifetime      = 12.0
	PowerUpMinimumInterval    = 7.0
	PowerUpMaximumInterval    = 11.0
	PowerUpRetryInterval      = 1.0
	PowerUpSpawnAttempts      = 80
	PowerUpTokenRadius        = 16.0
	PowerUpAreaInsetX         = 48.0
	PowerUpAreaBottomInset    = 44.0
	PowerUpAreaTopInset       = 80.0
	PowerUpStartClearance     = 80.0
	PowerUpStartClearanceRate = 0.22
	PowerUpTankClearance      = 50.0
	PowerUpObstacleClearance  = 26.0
	RapidFireCooldown         = 0.16
)

// Durations in physics ticks. The server is the reference, so every timer is
// a whole tick count: the seconds above rounded up to the first tick at which
// the Swift engine's "elapsed >= duration" check holds.
const (
	FireCooldownTicks         = 70    // 0.58 s
	RapidFireCooldownTicks    = 20    // 0.16 s
	HitProtectionTicks        = 44    // 0.36 s
	ShellLifetimeTicks        = 336   // 2.8 s
	RocketLifetimeTicks       = 384   // 3.2 s
	PowerUpDurationTicks      = 960   // 8 s
	PowerUpTokenLifetimeTicks = 1440  // 12 s
	PowerUpMinimumTicks       = 840   // 7 s
	PowerUpMaximumTicks       = 1320  // 11 s
	PowerUpRetryTicks         = 120   // 1 s
	RoundCountdownTicks       = 360   // RoundCountdownDuration
	RoundReadyWindowTicks     = 3600  // RoundReadyWindowDuration
	RoundReadyIdleTicks       = 14400 // RoundReadyIdleLimit
	MaximumMatchTicks         = 36000 // MaximumMatchDuration
	EventWindowTicks          = 120   // EventWindow
)

// SpreadAngles are the barrel offsets of one spread-shot volley.
func SpreadAngles() []float64 { return []float64{-0.28, -0.14, 0, 0.14, 0.28} }

type Phase = module.Phase

const (
	PhaseCountdown  = module.PhaseCountdown
	PhasePlaying    = module.PhasePlaying
	PhaseRecovering = module.PhaseRecovering
	PhaseFinalizing = module.PhaseFinalizing
	PhaseEnded      = module.PhaseEnded
	PhaseCancelled  = module.PhaseCancelled
)

// RoundPhase is the match's own flow while the core phase is playing.
type RoundPhase string

const (
	RoundCountdown RoundPhase = "roundCountdown"
	RoundPlaying   RoundPhase = "roundPlaying"
	RoundReady     RoundPhase = "roundReady"
	MatchEnded     RoundPhase = "matchEnded"
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

type Color string

const (
	ColorPlum    Color = "plum"
	ColorOrange  Color = "orange"
	ColorBlue    Color = "blue"
	ColorOlive   Color = "olive"
	ColorMustard Color = "mustard"
	ColorCrimson Color = "crimson"
	ColorSky     Color = "sky"
	ColorIvory   Color = "ivory"
)

// PlayerColors are assigned in seat order.
func PlayerColors() []Color {
	return []Color{ColorPlum, ColorOrange, ColorBlue, ColorOlive, ColorMustard, ColorCrimson, ColorSky, ColorIvory}
}

type MapID string

const (
	MapLivingRoom MapID = "livingRoom"
	MapCorridors  MapID = "corridors"
	MapWarehouse  MapID = "warehouse"
	MapCourtyard  MapID = "courtyard"
	MapBasement   MapID = "basement"
)

type PowerUpKind string

const (
	PowerUpRocket     PowerUpKind = "rocket"
	PowerUpRapidFire  PowerUpKind = "rapidFire"
	PowerUpSpreadShot PowerUpKind = "spreadShot"
	PowerUpShield     PowerUpKind = "shield"
	PowerUpAim        PowerUpKind = "aim"
)

// PowerUpKinds is the order a fresh joker bag is shuffled from.
func PowerUpKinds() []PowerUpKind {
	return []PowerUpKind{PowerUpRocket, PowerUpRapidFire, PowerUpSpreadShot, PowerUpShield, PowerUpAim}
}

type EventKind string

const (
	EventFired          EventKind = "fired"
	EventHit            EventKind = "hit"
	EventShielded       EventKind = "shielded"
	EventCrateBroken    EventKind = "crateBroken"
	EventExplosion      EventKind = "explosion"
	EventEliminated     EventKind = "eliminated"
	EventJokerCollected EventKind = "jokerCollected"
	EventRoundEnded     EventKind = "roundEnded"
)

type ResultStatus string

const (
	ResultCompleted ResultStatus = "completed"
	ResultCancelled ResultStatus = "cancelled"
)

type EndReason string

const (
	EndChampionDecided         EndReason = "championDecided"
	EndInsufficientPlayers     EndReason = "insufficientPlayers"
	EndCancelledByUser         EndReason = module.EndCancelledByUser
	EndSessionExpired          EndReason = "sessionExpired"
	EndRecoveryFailed          EndReason = module.EndRecoveryFailed
	EndCoordinationUnavailable EndReason = "coordinationUnavailable"
	EndRuntimeOverloaded       EndReason = "runtimeOverloaded"
)
