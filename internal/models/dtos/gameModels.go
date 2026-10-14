package dtos

import (
	"encoding/json"
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
		SessionId:         snapshot.SessionID,
		HouseId:           snapshot.HouseID,
		GameKey:           snapshot.GameKey,
		ProtocolVersion:   snapshot.ProtocolVersion,
		Mode:              string(snapshot.Mode),
		State:             string(snapshot.State),
		Rules:             gameRulesModel(snapshot.Rules),
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

// GameCatalogEntryModel is a game the house can start, with the rules and
// client settings a new session would get.
type GameCatalogEntryModel struct {
	GameKey         string                `json:"gameKey"`
	ProtocolVersion int                   `json:"protocolVersion"`
	Mode            string                `json:"mode"`
	ConfigVersion   int64                 `json:"configVersion"`
	Rules           GameSessionRulesModel `json:"rules"`
	Settings        any                   `json:"settings" swaggertype:"object"`
}

// GameConfigModel is a game's full operator config.
type GameConfigModel struct {
	GameKey         string                `json:"gameKey"`
	ProtocolVersion int                   `json:"protocolVersion"`
	Mode            string                `json:"mode"`
	Version         int64                 `json:"version"`
	Enabled         bool                  `json:"enabled"`
	HouseIds        []string              `json:"houseIds"`
	Rules           GameSessionRulesModel `json:"rules"`
	Settings        json.RawMessage       `json:"settings" swaggertype:"object"`
}

// UpdateGameConfigModel replaces a game's config. Omitted rules or settings
// keys use the code defaults; expectedVersion is the version being replaced
// (0 when the game has no stored config yet).
type UpdateGameConfigModel struct {
	ExpectedVersion int64           `json:"expectedVersion" validate:"gte=0"`
	Enabled         bool            `json:"enabled"`
	HouseIds        []string        `json:"houseIds" validate:"omitempty,dive,len=24,alphanum"`
	Rules           json.RawMessage `json:"rules,omitempty" swaggertype:"object"`
	Settings        json.RawMessage `json:"settings,omitempty" swaggertype:"object"`
}

func gameRulesModel(rules gameDomain.SessionRules) GameSessionRulesModel {
	return GameSessionRulesModel{
		MinimumPlayers:          rules.MinimumPlayers,
		MaximumPlayers:          rules.MaximumPlayers,
		ReadyWindowMilliseconds: rules.ReadyWindowDuration.Milliseconds(),
		CountdownMilliseconds:   rules.CountdownDuration.Milliseconds(),
	}
}

func GameCatalogEntryToModel(definition gameDomain.GameDefinition, config gameDomain.GameConfig, settings any) GameCatalogEntryModel {
	return GameCatalogEntryModel{GameKey: definition.GameKey, ProtocolVersion: definition.ProtocolVersion, Mode: string(definition.Mode), ConfigVersion: config.Version, Rules: gameRulesModel(config.Rules), Settings: settings}
}

func GameConfigToModel(definition gameDomain.GameDefinition, config gameDomain.GameConfig) GameConfigModel {
	houseIDs := append([]string{}, config.HouseIDs...)
	return GameConfigModel{GameKey: definition.GameKey, ProtocolVersion: definition.ProtocolVersion, Mode: string(definition.Mode), Version: config.Version, Enabled: config.Enabled, HouseIds: houseIDs, Rules: gameRulesModel(config.Rules), Settings: config.Settings}
}
