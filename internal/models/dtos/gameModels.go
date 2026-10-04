package dtos

import (
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

type EnsureGameSessionModel struct {
	HouseId string `json:"houseId" validate:"required,len=24,alphanum"`
}

type GameSessionRulesModel struct {
	MinimumPlayers          int   `json:"minimumPlayers"`
	MaximumPlayers          int   `json:"maximumPlayers"`
	ReadyWindowMilliseconds int64 `json:"readyWindowMilliseconds"`
	CountdownMilliseconds   int64 `json:"countdownMilliseconds"`
}

type GameSessionPlayerModel struct {
	PlayerId string       `json:"playerId"`
	State    string       `json:"state"`
	JoinedAt UTCDateTime  `json:"joinedAt"`
	ReadyAt  *UTCDateTime `json:"readyAt,omitempty"`
	LeftAt   *UTCDateTime `json:"leftAt,omitempty"`
}

type GameSessionResponseModel struct {
	SessionId         string                   `json:"sessionId"`
	HouseId           string                   `json:"houseId"`
	GameKey           string                   `json:"gameKey"`
	ProtocolVersion   int                      `json:"protocolVersion"`
	Mode              string                   `json:"mode"`
	State             string                   `json:"state"`
	Rules             GameSessionRulesModel    `json:"rules"`
	Players           []GameSessionPlayerModel `json:"players"`
	CreatedBy         string                   `json:"createdBy"`
	CreatedAt         UTCDateTime              `json:"createdAt"`
	UpdatedAt         UTCDateTime              `json:"updatedAt"`
	ReadyWindowEndsAt *UTCDateTime             `json:"readyWindowEndsAt,omitempty"`
	CountdownEndsAt   *UTCDateTime             `json:"countdownEndsAt,omitempty"`
	StartedAt         *UTCDateTime             `json:"startedAt,omitempty"`
	EndedAt           *UTCDateTime             `json:"endedAt,omitempty"`
	EndReason         string                   `json:"endReason,omitempty"`
	Version           int64                    `json:"version"`
}

func GameSessionToResponseModel(snapshot gameDomain.SessionSnapshot) GameSessionResponseModel {
	players := make([]GameSessionPlayerModel, 0, len(snapshot.Players))
	for _, player := range snapshot.Players {
		players = append(players, GameSessionPlayerModel{
			PlayerId: player.PlayerID,
			State:    string(player.State),
			JoinedAt: NewUTCDateTime(player.JoinedAt),
			ReadyAt:  optionalUTCDateTime(player.ReadyAt),
			LeftAt:   optionalUTCDateTime(player.LeftAt),
		})
	}
	return GameSessionResponseModel{
		SessionId:       snapshot.SessionID,
		HouseId:         snapshot.HouseID,
		GameKey:         snapshot.GameKey,
		ProtocolVersion: snapshot.ProtocolVersion,
		Mode:            string(snapshot.Mode),
		State:           string(snapshot.State),
		Rules: GameSessionRulesModel{
			MinimumPlayers:          snapshot.Rules.MinimumPlayers,
			MaximumPlayers:          snapshot.Rules.MaximumPlayers,
			ReadyWindowMilliseconds: snapshot.Rules.ReadyWindowDuration.Milliseconds(),
			CountdownMilliseconds:   snapshot.Rules.CountdownDuration.Milliseconds(),
		},
		Players:           players,
		CreatedBy:         snapshot.CreatedBy,
		CreatedAt:         NewUTCDateTime(snapshot.CreatedAt),
		UpdatedAt:         NewUTCDateTime(snapshot.UpdatedAt),
		ReadyWindowEndsAt: optionalUTCDateTime(snapshot.ReadyWindowEndsAt),
		CountdownEndsAt:   optionalUTCDateTime(snapshot.CountdownEndsAt),
		StartedAt:         optionalUTCDateTime(snapshot.StartedAt),
		EndedAt:           optionalUTCDateTime(snapshot.EndedAt),
		EndReason:         snapshot.EndReason,
		Version:           snapshot.Version,
	}
}

func optionalUTCDateTime(value time.Time) *UTCDateTime {
	if value.IsZero() {
		return nil
	}
	result := NewUTCDateTime(value)
	return &result
}
