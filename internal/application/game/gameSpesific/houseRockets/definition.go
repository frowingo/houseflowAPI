package houseRockets

import (
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

const (
	GameKey                       = "houseRockets"
	ProtocolVersion               = 2
	CourseVersion                 = 1
	ContractRevision              = "houseRockets.v2.1"
	MinimumPlayers                = 2
	MaximumPlayers                = 8
	ReadyWindowDuration           = 30 * time.Second
	CountdownDuration             = 3 * time.Second
	MaximumMatchDuration          = 5 * time.Minute
	ControlHeartbeatInterval      = 2 * time.Second
	ControlTimeout                = 6 * time.Second
	ReconnectGraceDuration        = 10 * time.Second
	PhysicsRateHz                 = 120
	SchedulingRateHz              = 60
	SnapshotRateHz                = 20
	MaximumInputRateHz            = 20
	MaximumSteerMessagesPerSecond = 30
	BaseSpeed                     = 300.0
	TrackHeight                   = 360.0
	RocketRadius                  = 10.0
	SpawnX                        = 250.0
	LeaderAnchorX                 = 420.0
	ViewportWidth                 = 1200.0
	FirstGateX                    = 900.0
	GateSpacing                   = 640.0
	TransitionIntervalSeconds     = 25.0
	TransitionDurationSeconds     = 3.0
	SpeedFieldRadius              = 15.4
	BoostMultiplier               = 1.45
	BoostDurationSeconds          = 1.3
	SlowMultiplier                = 0.65
	SlowDurationSeconds           = 1.4
	BoostPeriodSeconds            = 3.0
	SlowPeriodSeconds             = 4.4 / 1.2
)

type Phase string

const (
	PhaseCountdown  Phase = "countdown"
	PhasePlaying    Phase = "playing"
	PhaseRecovering Phase = "recovering"
	PhaseFinalizing Phase = "finalizing"
	PhaseEnded      Phase = "ended"
	PhaseCancelled  Phase = "cancelled"
)

type Color string

const (
	ColorMint   Color = "mint"
	ColorCoral  Color = "coral"
	ColorBlue   Color = "blue"
	ColorGold   Color = "gold"
	ColorViolet Color = "violet"
	ColorOrange Color = "orange"
	ColorPink   Color = "pink"
	ColorTeal   Color = "teal"
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

func PlayerColors() []Color {
	return []Color{ColorMint, ColorCoral, ColorBlue, ColorGold, ColorViolet, ColorOrange, ColorPink, ColorTeal}
}

type SpeedEffect string

const (
	EffectBoost SpeedEffect = "boost"
	EffectSlow  SpeedEffect = "slow"
)

type EliminationReason string

const (
	EliminationBehindCamera      EliminationReason = "behindCamera"
	EliminationForfeit           EliminationReason = "forfeit"
	EliminationConnectionExpired EliminationReason = "connectionExpired"
	EliminationMembershipRevoked EliminationReason = "membershipRevoked"
)

type ResultStatus string

const (
	ResultCompleted ResultStatus = "completed"
	ResultCancelled ResultStatus = "cancelled"
)

type EndReason string

const (
	EndLastSurvivor            EndReason = "lastSurvivor"
	EndSimultaneousElimination EndReason = "simultaneousElimination"
	EndInsufficientPlayers     EndReason = "insufficientPlayers"
	EndCancelledByUser         EndReason = "cancelledByUser"
	EndSessionExpired          EndReason = "sessionExpired"
	EndRecoveryFailed          EndReason = "recoveryFailed"
	EndCoordinationUnavailable EndReason = "coordinationUnavailable"
	EndRuntimeOverloaded       EndReason = "runtimeOverloaded"
)
