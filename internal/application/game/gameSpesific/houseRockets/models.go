package houseRockets

import (
	"math"
	"strings"
	"time"
)

type HouseRocketsSteerModel struct {
	ControlGeneration string  `json:"controlGeneration"`
	InputSequence     int64   `json:"inputSequence"`
	Heading           float64 `json:"heading"`
}

func (model HouseRocketsSteerModel) Valid() bool {
	return model.ControlGeneration != "" && len(model.ControlGeneration) <= 128 &&
		strings.TrimSpace(model.ControlGeneration) == model.ControlGeneration && model.InputSequence > 0 &&
		!math.IsNaN(model.Heading) && !math.IsInf(model.Heading, 0)
}

type HouseRocketsRuntimeSettingsModel struct {
	PhysicsRateHz                 int   `json:"physicsRateHz"`
	SchedulingRateHz              int   `json:"schedulingRateHz"`
	SnapshotRateHz                int   `json:"snapshotRateHz"`
	MaximumInputRateHz            int   `json:"maximumInputRateHz"`
	MaximumSteerMessagesPerSecond int   `json:"maximumSteerMessagesPerSecond"`
	ControlHeartbeatMilliseconds  int64 `json:"controlHeartbeatMilliseconds"`
	ControlTimeoutMilliseconds    int64 `json:"controlTimeoutMilliseconds"`
	ReconnectGraceMilliseconds    int64 `json:"reconnectGraceMilliseconds"`
	MaximumMatchMilliseconds      int64 `json:"maximumMatchMilliseconds"`
}

func HouseRocketsRuntimeSettings() HouseRocketsRuntimeSettingsModel {
	return HouseRocketsRuntimeSettingsModel{
		PhysicsRateHz:                 PhysicsRateHz,
		SchedulingRateHz:              SchedulingRateHz,
		SnapshotRateHz:                SnapshotRateHz,
		MaximumInputRateHz:            MaximumInputRateHz,
		MaximumSteerMessagesPerSecond: MaximumSteerMessagesPerSecond,
		ControlHeartbeatMilliseconds:  ControlHeartbeatInterval.Milliseconds(),
		ControlTimeoutMilliseconds:    ControlTimeout.Milliseconds(),
		ReconnectGraceMilliseconds:    ReconnectGraceDuration.Milliseconds(),
		MaximumMatchMilliseconds:      MaximumMatchDuration.Milliseconds(),
	}
}

type HouseRocketsWelcomeModel struct {
	SessionID       string                           `json:"sessionId"`
	GameKey         string                           `json:"gameKey"`
	ConnectionID    string                           `json:"connectionId"`
	ProtocolVersion int                              `json:"protocolVersion"`
	CourseVersion   int                              `json:"courseVersion"`
	Settings        HouseRocketsRuntimeSettingsModel `json:"settings"`
}

type HouseRocketsControlGrantedModel struct {
	SessionID         string `json:"sessionId"`
	PlayerID          string `json:"playerId"`
	RuntimeEpoch      int64  `json:"runtimeEpoch"`
	ControlGeneration string `json:"controlGeneration"`
}

type HouseRocketsPlayerModel struct {
	PlayerID                   string             `json:"playerId"`
	DisplayName                string             `json:"displayName"`
	Color                      Color              `json:"color"`
	WorldX                     float64            `json:"worldX"`
	WorldY                     float64            `json:"worldY"`
	CourseHeading              float64            `json:"courseHeading"`
	IsAlive                    bool               `json:"isAlive"`
	Connected                  bool               `json:"connected"`
	Distance                   float64            `json:"distance"`
	SpeedEffect                *SpeedEffect       `json:"speedEffect"`
	EffectRemainingSeconds     float64            `json:"effectRemainingSeconds"`
	EliminatedAtTick           *int64             `json:"eliminatedAtTick"`
	EliminationReason          *EliminationReason `json:"eliminationReason"`
	LastProcessedInputSequence int64              `json:"lastProcessedInputSequence"`
	ControlGeneration          *string            `json:"controlGeneration"`
}

type HouseRocketsPassageSectionModel struct {
	OffsetX float64 `json:"offsetX"`
	LowerY  float64 `json:"lowerY"`
	UpperY  float64 `json:"upperY"`
}

type HouseRocketsGateModel struct {
	ID       string                            `json:"id"`
	WorldX   float64                           `json:"worldX"`
	Sections []HouseRocketsPassageSectionModel `json:"sections"`
}

type HouseRocketsSpeedFieldModel struct {
	ID            string      `json:"id"`
	WorldX        float64     `json:"worldX"`
	Effect        SpeedEffect `json:"effect"`
	Phase         float64     `json:"phase"`
	PeriodSeconds float64     `json:"periodSeconds"`
}

type HouseRocketsSnapshotModel struct {
	SessionID       string                        `json:"sessionId"`
	GameKey         string                        `json:"gameKey"`
	RuntimeEpoch    int64                         `json:"runtimeEpoch"`
	StateSequence   int64                         `json:"stateSequence"`
	Tick            int64                         `json:"tick"`
	ElapsedSeconds  float64                       `json:"elapsedSeconds"`
	Phase           Phase                         `json:"phase"`
	CourseVersion   int                           `json:"courseVersion"`
	CameraX         float64                       `json:"cameraX"`
	CourseAngle     float64                       `json:"courseAngle"`
	Players         []HouseRocketsPlayerModel     `json:"players"`
	Gates           []HouseRocketsGateModel       `json:"gates"`
	SpeedFields     []HouseRocketsSpeedFieldModel `json:"speedFields"`
	CountdownEndsAt *time.Time                    `json:"countdownEndsAt"`
	WinnerID        *string                       `json:"winnerId"`
}

type HouseRocketsPlayerResultModel struct {
	PlayerID          string             `json:"playerId"`
	Rank              *int               `json:"rank"`
	EliminatedAtTick  *int64             `json:"eliminatedAtTick"`
	EliminationReason *EliminationReason `json:"eliminationReason"`
	Distance          float64            `json:"distance"`
}

type HouseRocketsResultModel struct {
	SessionID       string                          `json:"sessionId"`
	HouseID         string                          `json:"houseId"`
	GameKey         string                          `json:"gameKey"`
	ProtocolVersion int                             `json:"protocolVersion"`
	CourseVersion   int                             `json:"courseVersion"`
	Status          ResultStatus                    `json:"status"`
	EndReason       EndReason                       `json:"endReason"`
	WinnerID        *string                         `json:"winnerId"`
	StartedAt       *time.Time                      `json:"startedAt"`
	EndedAt         time.Time                       `json:"endedAt"`
	DurationSeconds float64                         `json:"durationSeconds"`
	Players         []HouseRocketsPlayerResultModel `json:"players"`
}
