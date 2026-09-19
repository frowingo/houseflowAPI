package database

import (
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

const GameSessionCollectionName = "GameSession"

type gameSessionRulesDocument struct {
	MinimumPlayers          int   `bson:"minimumPlayers"`
	MaximumPlayers          int   `bson:"maximumPlayers"`
	ReadyWindowMilliseconds int64 `bson:"readyWindowMilliseconds"`
	CountdownMilliseconds   int64 `bson:"countdownMilliseconds"`
}

type gameSessionPlayerDocument struct {
	PlayerID string                 `bson:"playerId"`
	State    gameDomain.PlayerState `bson:"state"`
	JoinedAt time.Time              `bson:"joinedAt"`
	ReadyAt  *time.Time             `bson:"readyAt,omitempty"`
	LeftAt   *time.Time             `bson:"leftAt,omitempty"`
}

type gameSessionDocument struct {
	SessionID         string                      `bson:"_id"`
	HouseID           string                      `bson:"houseId"`
	GameKey           string                      `bson:"gameKey"`
	ProtocolVersion   int                         `bson:"protocolVersion"`
	Mode              gameDomain.GameMode         `bson:"mode"`
	State             gameDomain.SessionState     `bson:"state"`
	Rules             gameSessionRulesDocument    `bson:"rules"`
	Players           []gameSessionPlayerDocument `bson:"players"`
	CreatedBy         string                      `bson:"createdBy"`
	CreatedAt         time.Time                   `bson:"createdAt"`
	UpdatedAt         time.Time                   `bson:"updatedAt"`
	ReadyWindowEndsAt *time.Time                  `bson:"readyWindowEndsAt,omitempty"`
	CountdownEndsAt   *time.Time                  `bson:"countdownEndsAt,omitempty"`
	StartedAt         *time.Time                  `bson:"startedAt,omitempty"`
	EndedAt           *time.Time                  `bson:"endedAt,omitempty"`
	EndReason         string                      `bson:"endReason,omitempty"`
	Version           int64                       `bson:"version"`
}

func (gameSessionDocument) CollectionName() string {
	return GameSessionCollectionName
}

func gameSessionDocumentFromSnapshot(snapshot gameDomain.SessionSnapshot) gameSessionDocument {
	players := make([]gameSessionPlayerDocument, 0, len(snapshot.Players))
	for _, player := range snapshot.Players {
		players = append(players, gameSessionPlayerDocument{
			PlayerID: player.PlayerID,
			State:    player.State,
			JoinedAt: player.JoinedAt,
			ReadyAt:  optionalTime(player.ReadyAt),
			LeftAt:   optionalTime(player.LeftAt),
		})
	}
	return gameSessionDocument{
		SessionID:       snapshot.SessionID,
		HouseID:         snapshot.HouseID,
		GameKey:         snapshot.GameKey,
		ProtocolVersion: snapshot.ProtocolVersion,
		Mode:            snapshot.Mode,
		State:           snapshot.State,
		Rules: gameSessionRulesDocument{
			MinimumPlayers:          snapshot.Rules.MinimumPlayers,
			MaximumPlayers:          snapshot.Rules.MaximumPlayers,
			ReadyWindowMilliseconds: snapshot.Rules.ReadyWindowDuration.Milliseconds(),
			CountdownMilliseconds:   snapshot.Rules.CountdownDuration.Milliseconds(),
		},
		Players:           players,
		CreatedBy:         snapshot.CreatedBy,
		CreatedAt:         snapshot.CreatedAt,
		UpdatedAt:         snapshot.UpdatedAt,
		ReadyWindowEndsAt: optionalTime(snapshot.ReadyWindowEndsAt),
		CountdownEndsAt:   optionalTime(snapshot.CountdownEndsAt),
		StartedAt:         optionalTime(snapshot.StartedAt),
		EndedAt:           optionalTime(snapshot.EndedAt),
		EndReason:         snapshot.EndReason,
		Version:           snapshot.Version,
	}
}

func (document gameSessionDocument) snapshot() gameDomain.SessionSnapshot {
	players := make([]gameDomain.SessionPlayer, 0, len(document.Players))
	for _, player := range document.Players {
		players = append(players, gameDomain.SessionPlayer{
			PlayerID: player.PlayerID,
			State:    player.State,
			JoinedAt: player.JoinedAt,
			ReadyAt:  dereferenceTime(player.ReadyAt),
			LeftAt:   dereferenceTime(player.LeftAt),
		})
	}
	return gameDomain.SessionSnapshot{
		SessionID:       document.SessionID,
		HouseID:         document.HouseID,
		GameKey:         document.GameKey,
		ProtocolVersion: document.ProtocolVersion,
		Mode:            document.Mode,
		State:           document.State,
		Rules: gameDomain.SessionRules{
			MinimumPlayers:      document.Rules.MinimumPlayers,
			MaximumPlayers:      document.Rules.MaximumPlayers,
			ReadyWindowDuration: time.Duration(document.Rules.ReadyWindowMilliseconds) * time.Millisecond,
			CountdownDuration:   time.Duration(document.Rules.CountdownMilliseconds) * time.Millisecond,
		},
		Players:           players,
		CreatedBy:         document.CreatedBy,
		CreatedAt:         document.CreatedAt,
		UpdatedAt:         document.UpdatedAt,
		ReadyWindowEndsAt: dereferenceTime(document.ReadyWindowEndsAt),
		CountdownEndsAt:   dereferenceTime(document.CountdownEndsAt),
		StartedAt:         dereferenceTime(document.StartedAt),
		EndedAt:           dereferenceTime(document.EndedAt),
		EndReason:         document.EndReason,
		Version:           document.Version,
	}
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func dereferenceTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}
