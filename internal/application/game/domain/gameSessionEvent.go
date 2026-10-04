package domain

import "time"

type SessionEventType string

const (
	SessionCreated       SessionEventType = "session.created"
	PlayerJoined         SessionEventType = "session.playerJoined"
	PlayerReadyChanged   SessionEventType = "session.playerReadyChanged"
	PlayerLeftSession    SessionEventType = "session.playerLeft"
	PlayerExcluded       SessionEventType = "session.playerExcluded"
	ReadyWindowStarted   SessionEventType = "session.readyWindowStarted"
	ReadyWindowCancelled SessionEventType = "session.readyWindowCancelled"
	CountdownStarted     SessionEventType = "session.countdownStarted"
	GameStarted          SessionEventType = "session.gameStarted"
	GameFinished         SessionEventType = "session.gameFinished"
	GameCancelled        SessionEventType = "session.gameCancelled"
)

type SessionEvent struct {
	EventID    string           `json:"eventId"`
	SessionID  string           `json:"sessionId"`
	Type       SessionEventType `json:"type"`
	Version    int64            `json:"version"`
	OccurredAt time.Time        `json:"occurredAt"`
	PlayerID   string           `json:"playerId,omitempty"`
	State      SessionState     `json:"state"`
	Deadline   time.Time        `json:"deadline,omitempty"`
	Reason     string           `json:"reason,omitempty"`
}
