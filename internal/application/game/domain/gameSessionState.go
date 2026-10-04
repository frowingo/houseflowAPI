package domain

import (
	"errors"
	"time"
)

type GameMode string

const (
	RealtimeGame  GameMode = "realtime"
	TurnBasedGame GameMode = "turnBased"
)

type SessionState string

const (
	SessionLobby       SessionState = "lobby"
	SessionReadyWindow SessionState = "readyWindow"
	SessionCountdown   SessionState = "countdown"
	SessionRunning     SessionState = "running"
	SessionFinished    SessionState = "finished"
	SessionCancelled   SessionState = "cancelled"
)

type PlayerState string

const (
	PlayerWaiting  PlayerState = "waiting"
	PlayerReady    PlayerState = "ready"
	PlayerPlaying  PlayerState = "playing"
	PlayerFinished PlayerState = "finished"
	PlayerLeft     PlayerState = "left"
)

type SessionRules struct {
	MinimumPlayers      int
	MaximumPlayers      int
	ReadyWindowDuration time.Duration
	CountdownDuration   time.Duration
}

func (rules SessionRules) Validate() error {
	if rules.MinimumPlayers < 2 {
		return ErrMinimumPlayers
	}
	if rules.MaximumPlayers < rules.MinimumPlayers {
		return ErrMaximumPlayers
	}
	if rules.ReadyWindowDuration < 0 {
		return ErrReadyWindowDuration
	}
	if rules.CountdownDuration < 0 {
		return ErrCountdownDuration
	}
	return nil
}

type SessionPlayer struct {
	PlayerID string
	State    PlayerState
	JoinedAt time.Time
	ReadyAt  time.Time
	LeftAt   time.Time
}

type SessionSnapshot struct {
	SessionID         string
	HouseID           string
	GameKey           string
	ProtocolVersion   int
	Mode              GameMode
	State             SessionState
	Rules             SessionRules
	Players           []SessionPlayer
	CreatedBy         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ReadyWindowEndsAt time.Time
	CountdownEndsAt   time.Time
	StartedAt         time.Time
	EndedAt           time.Time
	EndReason         string
	Version           int64
}

var (
	ErrSessionIDRequired   = errors.New("game session ID is required")
	ErrHouseIDRequired     = errors.New("house ID is required")
	ErrGameKeyRequired     = errors.New("game key is required")
	ErrCreatedByRequired   = errors.New("session creator is required")
	ErrProtocolVersion     = errors.New("game protocol version must be greater than zero")
	ErrGameMode            = errors.New("unsupported game mode")
	ErrMinimumPlayers      = errors.New("minimum player count must be at least two")
	ErrMaximumPlayers      = errors.New("maximum player count must be greater than or equal to minimum player count")
	ErrReadyWindowDuration = errors.New("ready window duration cannot be negative")
	ErrCountdownDuration   = errors.New("countdown duration cannot be negative")
	ErrPlayerIDRequired    = errors.New("player ID is required")
	ErrPlayerAlreadyJoined = errors.New("player already joined the game session")
	ErrPlayerNotFound      = errors.New("player is not part of the game session")
	ErrSessionFull         = errors.New("game session is full")
	ErrJoinClosed          = errors.New("game session no longer accepts players")
	ErrReadyClosed         = errors.New("game session no longer accepts ready changes")
	ErrInvalidSessionState = errors.New("operation is not allowed in the current game session state")
	ErrTerminalSession     = errors.New("game session is already terminal")
	ErrEndReasonRequired   = errors.New("game session end reason is required")
	ErrTimestampRequired   = errors.New("operation timestamp is required")
	ErrTimestampOutOfOrder = errors.New("operation timestamp cannot be earlier than the latest session change")
	ErrSnapshotInvalid     = errors.New("game session snapshot is invalid")
)

func isSupportedGameMode(mode GameMode) bool {
	return mode == RealtimeGame || mode == TurnBasedGame
}

func isTerminalState(state SessionState) bool {
	return state == SessionFinished || state == SessionCancelled
}

func isSupportedSessionState(state SessionState) bool {
	switch state {
	case SessionLobby, SessionReadyWindow, SessionCountdown, SessionRunning, SessionFinished, SessionCancelled:
		return true
	default:
		return false
	}
}

func isSupportedPlayerState(state PlayerState) bool {
	switch state {
	case PlayerWaiting, PlayerReady, PlayerPlaying, PlayerFinished, PlayerLeft:
		return true
	default:
		return false
	}
}
