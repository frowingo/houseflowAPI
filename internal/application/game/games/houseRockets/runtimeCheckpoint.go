package houseRockets

import (
	"encoding/json"
	"strconv"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

const RuntimeCheckpointVersion = 1

func ValidateCheckpointSession(checkpoint RuntimeCheckpoint, session gameDomain.SessionSnapshot) error {
	if checkpoint.ProposedResult != nil {
		return ValidateResult(*checkpoint.ProposedResult, session)
	}
	world := checkpoint.World
	startedAt := world.StartedAt
	result := HouseRocketsResultModel{SessionID: world.SessionID, HouseID: world.HouseID, GameKey: GameKey, ProtocolVersion: ProtocolVersion, CourseVersion: CourseVersion, Status: ResultCancelled, EndReason: EndRecoveryFailed, StartedAt: &startedAt, EndedAt: startedAt.Add(time.Duration(world.Tick) * time.Second / PhysicsRateHz), DurationSeconds: float64(world.Tick) / PhysicsRateHz}
	for _, player := range world.Players {
		result.Players = append(result.Players, HouseRocketsPlayerResultModel{PlayerID: player.PlayerID, EliminatedAtTick: cloneValue(player.EliminatedAtTick), EliminationReason: cloneValue(player.EliminationReason), Distance: player.Distance()})
	}
	return ValidateResult(result, session)
}

type ControlCheckpoint struct {
	PlayerID       string    `json:"playerId"`
	ConnectionID   string    `json:"connectionId"`
	Generation     string    `json:"generation"`
	LastSeen       time.Time `json:"lastSeen"`
	GraceEndsAt    time.Time `json:"graceEndsAt"`
	HighestInput   int64     `json:"highestInput"`
	ProcessedInput int64     `json:"processedInput"`
}

type RuntimeCheckpoint struct {
	SchemaVersion  int                      `json:"schemaVersion"`
	Epoch          int64                    `json:"epoch"`
	Sequence       int64                    `json:"sequence"`
	CapturedAt     time.Time                `json:"capturedAt"`
	World          SimulationState          `json:"world"`
	Controls       []ControlCheckpoint      `json:"controls"`
	ProposedResult *HouseRocketsResultModel `json:"proposedResult"`
	Remainder      int64                    `json:"remainder"`
}

// CriticalKey excludes movement/input ACK changes. A control handover, an
// elimination or a terminal proposal must be protected before it is announced.
// It is computed for every published frame, so it is built in one buffer.
func (checkpoint RuntimeCheckpoint) CriticalKey() string {
	size := 16
	for index, player := range checkpoint.World.Players {
		size += len(player.PlayerID) + len(checkpoint.Controls[index].ConnectionID) + len(checkpoint.Controls[index].Generation) + 5
	}
	key := make([]byte, 0, size)
	for index, player := range checkpoint.World.Players {
		key = append(key, player.PlayerID...)
		key = append(key, ':')
		key = strconv.AppendBool(key, player.IsAlive)
		key = append(key, ':')
		key = append(key, checkpoint.Controls[index].ConnectionID...)
		key = append(key, ':')
		key = append(key, checkpoint.Controls[index].Generation...)
		key = append(key, ';')
	}
	if outcome := checkpoint.World.Outcome; outcome != nil {
		key = append(key, outcome.Status...)
		key = append(key, ':')
		key = append(key, outcome.EndReason...)
		if outcome.WinnerID != nil {
			key = append(key, ':')
			key = append(key, *outcome.WinnerID...)
		}
	}
	return string(key)
}

func (runtime *Runtime) checkpointLocked() RuntimeCheckpoint {
	checkpoint := RuntimeCheckpoint{SchemaVersion: RuntimeCheckpointVersion, Epoch: runtime.epoch, Sequence: runtime.sequence, CapturedAt: runtime.lastAdvance.UTC(), World: runtime.simulation.State(), Controls: make([]ControlCheckpoint, 0, len(runtime.controls)), ProposedResult: runtime.proposedResult, Remainder: runtime.remainder}
	for _, player := range checkpoint.World.Players {
		control := runtime.controls[player.PlayerID]
		checkpoint.Controls = append(checkpoint.Controls, ControlCheckpoint{PlayerID: player.PlayerID, ConnectionID: control.connectionID, Generation: control.generation, LastSeen: control.lastSeen.UTC(), GraceEndsAt: control.graceEndsAt.UTC(), HighestInput: control.highestInput, ProcessedInput: control.processedInput})
	}
	return checkpoint
}

func (runtime *Runtime) Checkpoint() RuntimeCheckpoint {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	return runtime.checkpointLocked()
}

// RestoreRuntime keeps physics frozen during ownership interruption. No queued
// steering is replayed and no old connection generation survives the new epoch.
func RestoreRuntime(checkpoint RuntimeCheckpoint, epoch int64, now time.Time) (*Runtime, error) {
	return restoreRuntime(DefaultSettings(), checkpoint, epoch, now)
}

func restoreRuntime(settings Settings, checkpoint RuntimeCheckpoint, epoch int64, now time.Time) (*Runtime, error) {
	if !validCheckpointHeader(checkpoint, epoch, now) {
		return nil, ErrInvalidSimulationState
	}
	simulation, err := RestoreSimulation(checkpoint.World)
	if err != nil {
		return nil, err
	}
	players := make([]PlayerIdentity, len(checkpoint.World.Players))
	for index, player := range checkpoint.World.Players {
		players[index] = PlayerIdentity{PlayerID: player.PlayerID, DisplayName: player.DisplayName}
	}
	runtime, err := newRuntime(settings, NewSimulationParams{SessionID: checkpoint.World.SessionID, HouseID: checkpoint.World.HouseID, StartedAt: checkpoint.World.StartedAt, Players: players}, epoch, now, checkpoint.Sequence)
	if err != nil {
		return nil, err
	}
	runtime.simulation, runtime.restored = simulation, true
	runtime.remainder = checkpoint.Remainder
	for index, saved := range checkpoint.Controls {
		if saved.PlayerID != players[index].PlayerID || saved.HighestInput < saved.ProcessedInput || saved.ProcessedInput < 0 || (saved.Generation == "") != (saved.ConnectionID == "") || saved.LastSeen.After(checkpoint.CapturedAt.Add(maximumCatchUp)) {
			return nil, ErrInvalidSimulationState
		}
		deadline := saved.GraceEndsAt
		if deadline.IsZero() {
			deadline = saved.LastSeen.Add(settings.presenceDeadline())
		}
		remaining := deadline.Sub(checkpoint.CapturedAt)
		if remaining > settings.presenceDeadline()+maximumCatchUp {
			return nil, ErrInvalidSimulationState
		}
		runtime.controls[saved.PlayerID] = &controller{graceEndsAt: now.Add(max(0, remaining))}
	}
	if checkpoint.World.Outcome != nil {
		if checkpoint.ProposedResult == nil {
			return nil, ErrInvalidSimulationState
		}
		expected, err := simulation.ProposeResult(checkpoint.ProposedResult.EndedAt)
		actualBytes, _ := json.Marshal(checkpoint.ProposedResult)
		expectedBytes, _ := json.Marshal(expected)
		if err != nil || string(actualBytes) != string(expectedBytes) {
			return nil, ErrInvalidSimulationState
		}
		runtime.terminal, runtime.proposedResult = true, checkpoint.ProposedResult
	} else if checkpoint.ProposedResult != nil {
		return nil, ErrInvalidSimulationState
	}
	runtime.publishFrame()
	return runtime, nil
}

// validCheckpointHeader accepts a checkpoint only from an older epoch, with a
// bounded sequence, a capture time inside the match and one control per player.
func validCheckpointHeader(checkpoint RuntimeCheckpoint, epoch int64, now time.Time) bool {
	ordered := checkpoint.Epoch > 0 && epoch > checkpoint.Epoch && checkpoint.Sequence > 0 && checkpoint.Sequence < 1<<62
	captured := !checkpoint.CapturedAt.IsZero() && !checkpoint.CapturedAt.Before(checkpoint.World.StartedAt) && !now.IsZero()
	scheduler := checkpoint.Remainder >= 0 && checkpoint.Remainder < int64(time.Second)
	return checkpoint.SchemaVersion == RuntimeCheckpointVersion && ordered && captured && scheduler && len(checkpoint.Controls) == len(checkpoint.World.Players)
}

// RecoveryFrame advertises frozen authoritative state while controls rebind.
func (runtime *Runtime) RecoveryFrame() RuntimeFrame {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	frame := runtime.buildFrame()
	if !runtime.terminal {
		frame.Phase = PhaseRecovering
	}
	return frame
}
