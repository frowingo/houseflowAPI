package houseTanks

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
}

// RuntimeCheckpoint is the recovery state. The match is encoded when the
// checkpoint is taken, so the core may store it after the runtime moved on.
type RuntimeCheckpoint struct {
	SchemaVersion  int                    `json:"schemaVersion"`
	Epoch          int64                  `json:"epoch"`
	Sequence       int64                  `json:"sequence"`
	CapturedAt     time.Time              `json:"capturedAt"`
	Session        RuntimeSession         `json:"session"`
	Match          json.RawMessage        `json:"match"`
	Controls       []ControlCheckpoint    `json:"controls"`
	ProposedResult *HouseTanksResultModel `json:"proposedResult"`
	Remainder      int64                  `json:"remainder"`
	criticalKey    string
}

// CriticalKey changes when a lost checkpoint would be visible: a control
// handover, a leave, a vote, a destroyed tank, a round or match change.
// Motion, shots, jokers and input ACKs are not critical.
func (checkpoint RuntimeCheckpoint) CriticalKey() string { return checkpoint.criticalKey }

func (runtime *Runtime) criticalKey() string {
	match := runtime.match
	key := make([]byte, 0, 32+len(match.Players)*96)
	for index, player := range match.Players {
		control := runtime.controls[player.PlayerID]
		key = append(key, player.PlayerID...)
		key = append(key, ':')
		key = strconv.AppendBool(key, player.HasLeft)
		key = append(key, ':')
		key = strconv.AppendInt(key, int64(player.RoundsWon), 10)
		key = append(key, ':')
		key = strconv.AppendBool(key, match.World.Tanks[index].Active)
		key = append(key, ':')
		key = strconv.AppendBool(key, player.RoundReady)
		key = append(key, ':')
		key = append(key, control.connectionID...)
		key = append(key, ':')
		key = append(key, control.generation...)
		key = append(key, ';')
	}
	key = append(key, match.RoundPhase...)
	key = append(key, ':')
	key = strconv.AppendInt(key, int64(match.RoundNumber), 10)
	if match.ReadyWindowEndsAtTick != nil {
		key = append(key, ":window"...)
	}
	if match.Ended() {
		key = append(key, ':')
		key = append(key, match.Status...)
		key = append(key, ':')
		key = append(key, match.EndReason...)
		key = append(key, ':')
		key = append(key, match.ChampionID...)
	}
	return string(key)
}

func (runtime *Runtime) checkpointLocked() RuntimeCheckpoint {
	encoded, _ := json.Marshal(runtime.match)
	checkpoint := RuntimeCheckpoint{SchemaVersion: RuntimeCheckpointVersion, Epoch: runtime.epoch, Sequence: runtime.sequence, CapturedAt: runtime.lastAdvance.UTC(),
		Session: runtime.session, Match: encoded, Controls: make([]ControlCheckpoint, 0, len(runtime.session.Players)), ProposedResult: runtime.proposedResult,
		Remainder: runtime.remainder, criticalKey: runtime.criticalKey()}
	for _, player := range runtime.session.Players {
		control := runtime.controls[player.PlayerID]
		checkpoint.Controls = append(checkpoint.Controls, ControlCheckpoint{PlayerID: player.PlayerID, ConnectionID: control.connectionID, Generation: control.generation,
			LastSeen: control.lastSeen.UTC(), HighestInput: control.highestInput, ProcessedInput: control.processedInput})
	}
	return checkpoint
}

func (runtime *Runtime) Checkpoint() RuntimeCheckpoint {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	return runtime.checkpointLocked()
}

// restoreRuntime resumes a checkpoint under a newer epoch. No ticks are spent
// on the ownership gap, no queued press is replayed and no old generation
// survives: every tank is a stationary target until its player binds again,
// and the match counts everyone as present while they do.
func RestoreRuntime(checkpoint RuntimeCheckpoint, epoch int64, now time.Time) (*Runtime, error) {
	return restoreRuntime(DefaultSettings(), checkpoint, epoch, now)
}

func restoreRuntime(settings Settings, checkpoint RuntimeCheckpoint, epoch int64, now time.Time) (*Runtime, error) {
	if !validCheckpointHeader(checkpoint, epoch, now) || settings.Validate() != nil {
		return nil, ErrInvalidMatch
	}
	match, err := DecodeMatch(checkpoint.Match)
	if err != nil {
		return nil, err
	}
	session := checkpoint.Session
	if match.SessionID != session.SessionID || !validID(session.HouseID) || len(match.Players) != len(session.Players) {
		return nil, ErrInvalidMatch
	}
	for index, player := range session.Players {
		saved := checkpoint.Controls[index]
		if match.Players[index].PlayerID != player.PlayerID || saved.PlayerID != player.PlayerID || saved.HighestInput < saved.ProcessedInput || saved.ProcessedInput < 0 ||
			(saved.Generation == "") != (saved.ConnectionID == "") || saved.LastSeen.After(checkpoint.CapturedAt.Add(maximumCatchUp)) {
			return nil, ErrInvalidMatch
		}
	}
	runtime := newRuntimeShell(settings, session, match, epoch, now, checkpoint.Sequence)
	runtime.remainder = checkpoint.Remainder
	if !match.Ended() {
		for _, player := range session.Players {
			_ = match.SetControl(player.PlayerID, false)
		}
		match.AwaitPlayers(match.Tick + match.Timing.PresenceGraceTicks)
	}
	if match.Ended() {
		if checkpoint.ProposedResult == nil {
			return nil, ErrInvalidMatch
		}
		expected, err := proposeResult(session, match, checkpoint.ProposedResult.EndedAt)
		actual, _ := json.Marshal(checkpoint.ProposedResult)
		wanted, _ := json.Marshal(expected)
		if err != nil || string(actual) != string(wanted) {
			return nil, ErrInvalidMatch
		}
		runtime.terminal, runtime.proposedResult = true, checkpoint.ProposedResult
	} else if checkpoint.ProposedResult != nil {
		return nil, ErrInvalidMatch
	}
	runtime.publishFrame()
	return runtime, nil
}

// validCheckpointHeader accepts a checkpoint only from an older epoch, with a
// bounded sequence, a capture time inside the match and one control per player.
func validCheckpointHeader(checkpoint RuntimeCheckpoint, epoch int64, now time.Time) bool {
	ordered := checkpoint.Epoch > 0 && epoch > checkpoint.Epoch && checkpoint.Sequence > 0 && checkpoint.Sequence < 1<<62
	captured := !checkpoint.CapturedAt.IsZero() && !checkpoint.CapturedAt.Before(checkpoint.Session.StartedAt) && !now.IsZero()
	scheduler := checkpoint.Remainder >= 0 && checkpoint.Remainder < int64(time.Second)
	return checkpoint.SchemaVersion == RuntimeCheckpointVersion && ordered && captured && scheduler && !checkpoint.Session.StartedAt.IsZero() &&
		len(checkpoint.Controls) == len(checkpoint.Session.Players)
}

// ValidateCheckpointSession checks the restored match against the stored
// session as the result it would end with.
func ValidateCheckpointSession(checkpoint RuntimeCheckpoint, session gameDomain.SessionSnapshot) error {
	if checkpoint.ProposedResult != nil {
		return ValidateResult(*checkpoint.ProposedResult, session)
	}
	match, err := DecodeMatch(checkpoint.Match)
	if err != nil {
		return err
	}
	endedAt := checkpoint.Session.StartedAt.Add(ticksDuration(match.Tick))
	return ValidateResult(matchResult(checkpoint.Session, match, ResultCancelled, EndRecoveryFailed, endedAt), session)
}

// RecoveryFrame advertises the restored state while controls rebind.
func (runtime *Runtime) RecoveryFrame() RuntimeFrame {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	frame := runtime.buildFrame()
	if !runtime.terminal {
		frame.Phase = PhaseRecovering
	}
	return frame
}
