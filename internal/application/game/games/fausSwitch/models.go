package fausSwitch

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
)

// The wire contract fausSwitch.v1. Optional values are pointers without
// omitempty: the client treats a missing key as an error, so an empty value
// is always written as null.

// FausSwitchFlipModel is a tap while holding control. ClientTick is the world
// tick on the player's screen; the server applies the flip there, at most
// MaximumRewindTicks back.
type FausSwitchFlipModel struct {
	ControlGeneration string `json:"controlGeneration"`
	InputSequence     int64  `json:"inputSequence"`
	ClientTick        int64  `json:"clientTick"`
}

func (model FausSwitchFlipModel) Valid() bool {
	return validControlGeneration(model.ControlGeneration) && model.InputSequence > 0 && model.ClientTick >= 0
}

// FausSwitchSelectColorModel is the lobby claim of a robot color.
type FausSwitchSelectColorModel struct {
	Color Color `json:"color"`
}

func (model FausSwitchSelectColorModel) Valid() bool { return model.Color.Valid() }

// DecodeFlip strictly decodes a fausSwitch.flip payload.
func DecodeFlip(payload json.RawMessage) (FausSwitchFlipModel, error) {
	var flip FausSwitchFlipModel
	if err := helpers.DecodeStrictObject(payload, &flip, nil, "controlGeneration", "inputSequence", "clientTick"); err != nil {
		return FausSwitchFlipModel{}, errors.Join(ErrInvalidInput, err)
	}
	if !flip.Valid() {
		return FausSwitchFlipModel{}, ErrInvalidInput
	}
	return flip, nil
}

// DecodeSelectColor strictly decodes a fausSwitch.selectColor payload.
func DecodeSelectColor(payload json.RawMessage) (FausSwitchSelectColorModel, error) {
	var selection FausSwitchSelectColorModel
	if err := helpers.DecodeStrictObject(payload, &selection, nil, "color"); err != nil {
		return FausSwitchSelectColorModel{}, errors.Join(ErrInvalidInput, err)
	}
	if !selection.Valid() {
		return FausSwitchSelectColorModel{}, ErrInvalidInput
	}
	return selection, nil
}

func validControlGeneration(generation string) bool {
	return generation != "" && len(generation) <= 128 && strings.TrimSpace(generation) == generation
}

// FausSwitchRuntimeSettingsModel is the catalog and welcome view of a
// session's settings.
type FausSwitchRuntimeSettingsModel struct {
	PhysicsRateHz                int   `json:"physicsRateHz"`
	SnapshotRateHz               int   `json:"snapshotRateHz"`
	MaximumInputRateHz           int   `json:"maximumInputRateHz"`
	MaximumRewindMilliseconds    int64 `json:"maximumRewindMilliseconds"`
	ControlHeartbeatMilliseconds int64 `json:"controlHeartbeatMilliseconds"`
	ControlTimeoutMilliseconds   int64 `json:"controlTimeoutMilliseconds"`
	ReconnectGraceMilliseconds   int64 `json:"reconnectGraceMilliseconds"`
	MaximumMatchMilliseconds     int64 `json:"maximumMatchMilliseconds"`
}

type FausSwitchWelcomeModel struct {
	SessionID       string                         `json:"sessionId"`
	GameKey         string                         `json:"gameKey"`
	ConnectionID    string                         `json:"connectionId"`
	ProtocolVersion int                            `json:"protocolVersion"`
	CourseVersion   int                            `json:"courseVersion"`
	Settings        FausSwitchRuntimeSettingsModel `json:"settings"`
}

// Welcome is the first message of a connection, with the session's frozen
// settings.
func Welcome(sessionID, connectionID string, settings json.RawMessage) FausSwitchWelcomeModel {
	return FausSwitchWelcomeModel{SessionID: sessionID, GameKey: GameKey, ConnectionID: connectionID,
		ProtocolVersion: ProtocolVersion, CourseVersion: CourseVersion, Settings: RuntimeSettings(settings)}
}

type FausSwitchControlGrantedModel = module.ControlGrant

// FausSwitchRunnerModel is one robot. Its physical fields are the whole state
// the shared simulation needs: every client steps it forward exactly as the
// server does. UsedBoosts and UsedSlows are ascending pad ids, never null.
// ControlGeneration is set only on the watching connection's own robot while
// it holds control.
type FausSwitchRunnerModel struct {
	PlayerID                   string             `json:"playerId"`
	DisplayName                string             `json:"displayName"`
	Color                      Color              `json:"color"`
	X                          float64            `json:"x"`
	Y                          float64            `json:"y"`
	VelocityY                  float64            `json:"velocityY"`
	Gravity                    Gravity            `json:"gravity"`
	BoostedUntilTick           int64              `json:"boostedUntilTick"`
	UsedBoosts                 []int              `json:"usedBoosts"`
	SlowedUntilTick            int64              `json:"slowedUntilTick"`
	UsedSlows                  []int              `json:"usedSlows"`
	Status                     RunnerStatus       `json:"status"`
	FinishedAtTick             *int64             `json:"finishedAtTick"`
	EliminatedAtTick           *int64             `json:"eliminatedAtTick"`
	EliminationReason          *EliminationReason `json:"eliminationReason"`
	Connected                  bool               `json:"connected"`
	LastProcessedInputSequence int64              `json:"lastProcessedInputSequence"`
	ControlGeneration          *string            `json:"controlGeneration"`
}

// FausSwitchSnapshotModel is the public state of a race. Runners are in seat
// order (join order), which is also the order the simulation steps them.
// RunnerScale is 1, or CrowdedRunnerScale for a race that started with more
// than CrowdedRunnerThreshold robots. CountdownEndsAt is set only before the
// start (tick 0); WinnerID only once the result is committed.
type FausSwitchSnapshotModel struct {
	SessionID       string                  `json:"sessionId"`
	GameKey         string                  `json:"gameKey"`
	RuntimeEpoch    int64                   `json:"runtimeEpoch"`
	StateSequence   int64                   `json:"stateSequence"`
	Tick            int64                   `json:"tick"`
	ElapsedSeconds  float64                 `json:"elapsedSeconds"`
	Phase           Phase                   `json:"phase"`
	CourseVersion   int                     `json:"courseVersion"`
	RunnerScale     float64                 `json:"runnerScale"`
	Runners         []FausSwitchRunnerModel `json:"runners"`
	CountdownEndsAt *time.Time              `json:"countdownEndsAt"`
	WinnerID        *string                 `json:"winnerId"`
}

// FausSwitchPlayerResultModel is one robot's final standing. Distance is the
// robot's last course x (not measured from the start line); Rank is null in a
// cancelled race.
type FausSwitchPlayerResultModel struct {
	PlayerID          string             `json:"playerId"`
	Rank              *int               `json:"rank"`
	FinishedAtTick    *int64             `json:"finishedAtTick"`
	EliminatedAtTick  *int64             `json:"eliminatedAtTick"`
	EliminationReason *EliminationReason `json:"eliminationReason"`
	Distance          float64            `json:"distance"`
}

// FausSwitchResultModel is the immutable outcome. Only raceComplete and
// timeLimit complete a race; StartedAt is null for a race that never started.
// WinnerID is the sole rank-one finisher, or null.
type FausSwitchResultModel struct {
	SessionID       string                        `json:"sessionId"`
	HouseID         string                        `json:"houseId"`
	GameKey         string                        `json:"gameKey"`
	ProtocolVersion int                           `json:"protocolVersion"`
	CourseVersion   int                           `json:"courseVersion"`
	Status          ResultStatus                  `json:"status"`
	EndReason       EndReason                     `json:"endReason"`
	WinnerID        *string                       `json:"winnerId"`
	StartedAt       *time.Time                    `json:"startedAt"`
	EndedAt         time.Time                     `json:"endedAt"`
	DurationSeconds float64                       `json:"durationSeconds"`
	Players         []FausSwitchPlayerResultModel `json:"players"`
}
