package fausSwitch

import (
	"encoding/json"
	"strconv"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

const RuntimeCheckpointVersion = 1

type ControlCheckpoint struct {
	PlayerID       string    `json:"playerId"`
	ConnectionID   string    `json:"connectionId"`
	Generation     string    `json:"generation"`
	LastSeen       time.Time `json:"lastSeen"`
	HighestInput   int64     `json:"highestInput"`
	ProcessedInput int64     `json:"processedInput"`
	Left           bool      `json:"left"`
}

// RuntimeCheckpoint is the recovery state: the roster with its final colors,
// the live race (simulationState: no rewind ring, no input logs), who left
// and the proposed result once there is one. Every part is a value taken
// when the checkpoint is, so the core may store it after the race moved on.
type RuntimeCheckpoint struct {
	SchemaVersion  int                    `json:"schemaVersion"`
	Epoch          int64                  `json:"epoch"`
	Sequence       int64                  `json:"sequence"`
	CapturedAt     time.Time              `json:"capturedAt"`
	Session        RuntimeSession         `json:"session"`
	Race           simulationState        `json:"race"`
	Controls       []ControlCheckpoint    `json:"controls"`
	ProposedResult *FausSwitchResultModel `json:"proposedResult"`
	Remainder      int64                  `json:"remainder"`
	criticalKey    string
}

// CriticalKey changes when a lost checkpoint would be visible: a control
// handover, a player leaving, a robot finishing or crashing, the race
// stopping or ending. Motion and flips are not critical: a lost stretch of
// them is replayed by the players, not by the server.
func (checkpoint RuntimeCheckpoint) CriticalKey() string { return checkpoint.criticalKey }

// criticalKey is the key a checkpoint keeps.
func (runtime *Runtime) criticalKey() string {
	runtime.criticalKeys[1] = runtime.appendCriticalKey(runtime.criticalKeys[1][:0])
	return string(runtime.criticalKeys[1])
}

func (runtime *Runtime) appendCriticalKey(key []byte) []byte {
	for seat := range runtime.controls {
		control, robot := &runtime.controls[seat], &runtime.race.world[seat]
		key = append(key, control.connectionID...)
		key = append(key, ':')
		key = append(key, control.generation...)
		key = append(key, ':')
		key = strconv.AppendBool(key, control.left)
		key = append(key, ':')
		key = strconv.AppendInt(key, int64(robot.status), 10)
		key = append(key, ';')
	}
	key = strconv.AppendBool(key, runtime.race.stoppedAt != noTick)
	if outcome := runtime.race.outcome; outcome != nil {
		key = append(key, ':')
		key = append(key, outcome.status...)
		key = append(key, ':')
		key = append(key, outcome.endReason...)
	}
	return key
}

func (runtime *Runtime) checkpointLocked(state simulationState) RuntimeCheckpoint {
	checkpoint := RuntimeCheckpoint{SchemaVersion: RuntimeCheckpointVersion, Epoch: runtime.epoch, Sequence: runtime.sequence, CapturedAt: runtime.lastAdvance.UTC(),
		Session: runtime.session, Race: state, Controls: make([]ControlCheckpoint, len(runtime.controls)), ProposedResult: runtime.proposedResult,
		Remainder: runtime.remainder, criticalKey: runtime.criticalKey()}
	for seat, player := range runtime.session.Players {
		control := &runtime.controls[seat]
		checkpoint.Controls[seat] = ControlCheckpoint{PlayerID: player.PlayerID, ConnectionID: control.connectionID, Generation: control.generation,
			LastSeen: control.lastSeen.UTC(), HighestInput: control.highestInput, ProcessedInput: control.processedInput, Left: control.left}
	}
	return checkpoint
}

func (runtime *Runtime) Checkpoint() RuntimeCheckpoint {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	return runtime.checkpointLocked(runtime.race.state())
}

// restoreRuntime resumes a checkpoint under a newer epoch. No ticks are spent
// on the ownership gap and no old control survives: robots run without input
// until their players bind again, and no flip may reach back before the
// restored tick.
func restoreRuntime(settings Settings, checkpoint RuntimeCheckpoint, epoch int64, now time.Time) (*Runtime, error) {
	if !validCheckpointHeader(checkpoint, epoch, now) || settings.Validate() != nil || !validRuntimeSession(checkpoint.Session) {
		return nil, errInvalidSimulationState
	}
	race, err := restoreSimulation(checkpoint.Race)
	if err != nil || race.count != len(checkpoint.Session.Players) {
		return nil, errInvalidSimulationState
	}
	session := checkpoint.Session
	runtime := newRuntimeShell(settings, session, race, epoch, now, checkpoint.Sequence)
	runtime.remainder = checkpoint.Remainder
	for seat, player := range session.Players {
		saved := checkpoint.Controls[seat]
		if saved.PlayerID != player.PlayerID || saved.HighestInput < saved.ProcessedInput || saved.ProcessedInput < 0 ||
			(saved.Generation == "") != (saved.ConnectionID == "") || saved.LastSeen.After(checkpoint.CapturedAt.Add(maximumCatchUp)) ||
			(saved.Left && race.world[seat].running()) {
			return nil, errInvalidSimulationState
		}
		runtime.controls[seat].left = saved.Left
		runtime.controls[seat].processedInput = saved.ProcessedInput
	}
	if race.outcome != nil {
		if checkpoint.ProposedResult == nil {
			return nil, errInvalidSimulationState
		}
		expected, err := proposeResult(session, race, checkpoint.ProposedResult.EndedAt)
		actual, _ := json.Marshal(checkpoint.ProposedResult)
		wanted, _ := json.Marshal(expected)
		if err != nil || string(actual) != string(wanted) {
			return nil, errInvalidSimulationState
		}
		runtime.terminal, runtime.proposedResult = true, checkpoint.ProposedResult
	} else if checkpoint.ProposedResult != nil {
		return nil, errInvalidSimulationState
	}
	runtime.publishFrame()
	return runtime, nil
}

// validCheckpointHeader accepts a checkpoint only from an older epoch, with a
// bounded sequence, a capture time inside the race and one control per seat.
func validCheckpointHeader(checkpoint RuntimeCheckpoint, epoch int64, now time.Time) bool {
	ordered := checkpoint.SchemaVersion == RuntimeCheckpointVersion && checkpoint.Epoch > 0 && epoch > checkpoint.Epoch &&
		checkpoint.Sequence > 0 && checkpoint.Sequence < 1<<62
	captured := !checkpoint.CapturedAt.IsZero() && !checkpoint.CapturedAt.Before(checkpoint.Session.StartedAt) && !now.IsZero()
	scheduler := checkpoint.Remainder >= 0 && checkpoint.Remainder < int64(time.Second)
	return ordered && captured && scheduler && len(checkpoint.Controls) == len(checkpoint.Session.Players)
}

// ValidateCheckpointSession checks the restored race against the stored
// session as the result it would end with.
func ValidateCheckpointSession(checkpoint RuntimeCheckpoint, session gameDomain.SessionSnapshot) error {
	if checkpoint.ProposedResult != nil {
		return ValidateResult(*checkpoint.ProposedResult, session)
	}
	race, err := restoreSimulation(checkpoint.Race)
	if err != nil {
		return err
	}
	if err := race.cancel(EndRecoveryFailed); err != nil {
		return err
	}
	result, err := proposeResult(checkpoint.Session, race, checkpoint.Session.StartedAt.Add(ticksDuration(race.tick)))
	if err != nil {
		return err
	}
	return ValidateResult(result, session)
}

// RecoveryFrame advertises the restored race while controls rebind.
func (runtime *Runtime) RecoveryFrame() RuntimeFrame {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	frame := runtime.buildFrame()
	if !runtime.terminal {
		frame.Phase = PhaseRecovering
	}
	return frame
}
