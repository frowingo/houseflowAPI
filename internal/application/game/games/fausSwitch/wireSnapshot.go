package fausSwitch

import "time"

// The cross-instance frame is the race as the owner published it; every
// instance renders it into fausSwitch.snapshot messages (module.go).

type RuntimePlayerControl struct {
	PlayerID                   string `json:"playerId"`
	ConnectionID               string `json:"connectionId"`
	Connected                  bool   `json:"connected"`
	LastProcessedInputSequence int64  `json:"lastProcessedInputSequence"`
}

// RuntimeFrame is what the transport reads (the header fields) and what
// frameView renders. Controls are in seat order.
type RuntimeFrame struct {
	RuntimeEpoch    int64                  `json:"runtimeEpoch"`
	StateSequence   int64                  `json:"stateSequence"`
	Phase           Phase                  `json:"phase"`
	World           FrameWorld             `json:"world"`
	Controls        []RuntimePlayerControl `json:"controls"`
	CommittedResult *FausSwitchResultModel `json:"committedResult,omitempty"`
	CountdownEndsAt *time.Time             `json:"countdownEndsAt,omitempty"`
	Checkpoint      *RuntimeCheckpoint     `json:"-"`
}

// FrameWorld is the public race state of a frame.
type FrameWorld struct {
	SessionID   string        `json:"sessionId"`
	Tick        int64         `json:"tick"`
	RunnerScale float64       `json:"runnerScale"`
	Players     []FramePlayer `json:"players"`
}

// FramePlayer is a seat. IsAlive is what the transport reads: the player is
// still in the race (has not left), whatever happened to the robot. An
// eliminated player keeps control, so a late flip that rescues the robot
// still reaches the runtime.
type FramePlayer struct {
	PlayerID    string `json:"playerId"`
	DisplayName string `json:"displayName"`
	Color       Color  `json:"color"`
	IsAlive     bool   `json:"isAlive"`
	runnerState
}

func frameWorld(session RuntimeSession, state simulationState, left []bool) FrameWorld {
	world := FrameWorld{SessionID: session.SessionID, Tick: state.Tick, RunnerScale: runnerScale(len(state.Runners)), Players: make([]FramePlayer, len(state.Runners))}
	for seat, player := range session.Players {
		world.Players[seat] = FramePlayer{PlayerID: player.PlayerID, DisplayName: player.DisplayName, Color: player.Color, IsAlive: !left[seat], runnerState: state.Runners[seat]}
	}
	return world
}

// SnapshotForConnection is the public snapshot of a frame. The watching
// connection's own robot carries its controlGeneration only while that
// connection holds the robot's control; frameView splices it into the
// shared bytes, so this is built with an empty grant.
func SnapshotForConnection(frame RuntimeFrame, playerID, connectionID string, grant *FausSwitchControlGrantedModel) FausSwitchSnapshotModel {
	world := frame.World
	snapshot := FausSwitchSnapshotModel{SessionID: world.SessionID, GameKey: GameKey, RuntimeEpoch: frame.RuntimeEpoch, StateSequence: frame.StateSequence,
		Tick: world.Tick, ElapsedSeconds: float64(world.Tick) / PhysicsRateHz, Phase: frame.Phase, CourseVersion: CourseVersion, RunnerScale: world.RunnerScale,
		Runners: make([]FausSwitchRunnerModel, len(world.Players)), CountdownEndsAt: frame.CountdownEndsAt}
	if frame.CommittedResult != nil {
		snapshot.WinnerID = frame.CommittedResult.WinnerID
	}
	for seat, player := range world.Players {
		var control RuntimePlayerControl
		if seat < len(frame.Controls) {
			control = frame.Controls[seat]
		}
		robot := player.runnerState
		usedBoosts, usedSlows := robot.UsedBoosts, robot.UsedSlows
		if usedBoosts == nil {
			usedBoosts = []int{}
		}
		if usedSlows == nil {
			usedSlows = []int{}
		}
		runner := FausSwitchRunnerModel{PlayerID: player.PlayerID, DisplayName: player.DisplayName, Color: player.Color, X: robot.X, Y: robot.Y,
			VelocityY: robot.VelocityY, Gravity: robot.Gravity, BoostedUntilTick: robot.BoostedUntilTick, UsedBoosts: usedBoosts,
			SlowedUntilTick: robot.SlowedUntilTick, UsedSlows: usedSlows, Status: robot.Status, FinishedAtTick: robot.FinishedAtTick,
			EliminatedAtTick: robot.EliminatedAtTick, EliminationReason: robot.EliminationReason, Connected: control.Connected,
			LastProcessedInputSequence: control.LastProcessedInputSequence}
		if player.IsAlive && frame.Phase == PhasePlaying && grant != nil && grant.RuntimeEpoch == frame.RuntimeEpoch && grant.PlayerID == playerID &&
			player.PlayerID == playerID && control.ConnectionID == connectionID && control.Connected {
			generation := grant.ControlGeneration
			runner.ControlGeneration = &generation
		}
		snapshot.Runners[seat] = runner
	}
	return snapshot
}
