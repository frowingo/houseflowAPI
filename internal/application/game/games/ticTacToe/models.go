package ticTacToe

import "time"

type SettingsModel struct {
	TurnTimeoutMilliseconds int64 `json:"turnTimeoutMilliseconds"`
}

type WelcomeModel struct {
	SessionID       string        `json:"sessionId"`
	GameKey         string        `json:"gameKey"`
	ConnectionID    string        `json:"connectionId"`
	ProtocolVersion int           `json:"protocolVersion"`
	Settings        SettingsModel `json:"settings"`
}

// MoveModel is the client's move: the index of an empty cell, row by row.
type MoveModel struct {
	Cell int `json:"cell"`
}

type PlayerModel struct {
	PlayerID string `json:"playerId"`
	Mark     Mark   `json:"mark"`
}

// StateModel is the board as a player sees it after every change.
type StateModel struct {
	SessionID       string        `json:"sessionId"`
	GameKey         string        `json:"gameKey"`
	Phase           string        `json:"phase"`
	Board           [9]*Mark      `json:"board"`
	Players         []PlayerModel `json:"players"`
	CurrentPlayerID *string       `json:"currentPlayerId"`
	TurnEndsAt      *time.Time    `json:"turnEndsAt"`
	Moves           int           `json:"moves"`
	YourMark        *Mark         `json:"yourMark"`
}

const (
	PhasePlaying = "playing"
	PhaseEnded   = "ended"
)

type ResultPlayerModel struct {
	PlayerID string   `json:"playerId"`
	Mark     *Mark    `json:"mark"`
	Outcome  *Outcome `json:"outcome"`
}

// ResultModel is the immutable match result and its public wire model.
type ResultModel struct {
	SessionID     string              `json:"sessionId"`
	HouseID       string              `json:"houseId"`
	GameKey       string              `json:"gameKey"`
	SchemaVersion int                 `json:"schemaVersion"`
	Status        string              `json:"status"`
	EndReason     string              `json:"endReason"`
	StartedAt     time.Time           `json:"startedAt"`
	EndedAt       time.Time           `json:"endedAt"`
	WinnerID      *string             `json:"winnerId"`
	Players       []ResultPlayerModel `json:"players"`
	Board         [9]*Mark            `json:"board"`
	Moves         int                 `json:"moves"`
}

func boardModel(board [9]Mark) [9]*Mark {
	var model [9]*Mark
	for index, cell := range board {
		if cell != "" {
			mark := cell
			model[index] = &mark
		}
	}
	return model
}
