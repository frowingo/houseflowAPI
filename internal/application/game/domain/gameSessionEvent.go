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
	EventID    string
	SessionID  string
	Type       SessionEventType
	Version    int64
	OccurredAt time.Time
	PlayerID   string
	State      SessionState
	Deadline   time.Time
	Reason     string
}
