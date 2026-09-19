package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	EndReasonInsufficientPlayers = "insufficientPlayers"
	EndReasonCancelledByUser     = "cancelledByUser"
)

type NewSessionParams struct {
	SessionID       string
	HouseID         string
	GameKey         string
	ProtocolVersion int
	Mode            GameMode
	Rules           SessionRules
	CreatedBy       string
	CreatedAt       time.Time
}

type GameSession struct {
	sessionID         string
	houseID           string
	gameKey           string
	protocolVersion   int
	mode              GameMode
	state             SessionState
	rules             SessionRules
	players           []SessionPlayer
	createdBy         string
	createdAt         time.Time
	updatedAt         time.Time
	readyWindowEndsAt time.Time
	countdownEndsAt   time.Time
	startedAt         time.Time
	endedAt           time.Time
	endReason         string
	version           int64
	pendingEvents     []SessionEvent
}

func NewGameSession(params NewSessionParams) (*GameSession, error) {
	if err := validateNewSession(params); err != nil {
		return nil, err
	}
	createdAt := params.CreatedAt.UTC()
	session := &GameSession{
		sessionID:       params.SessionID,
		houseID:         params.HouseID,
		gameKey:         params.GameKey,
		protocolVersion: params.ProtocolVersion,
		mode:            params.Mode,
		state:           SessionLobby,
		rules:           params.Rules,
		players:         make([]SessionPlayer, 0, params.Rules.MaximumPlayers),
		createdBy:       params.CreatedBy,
		createdAt:       createdAt,
		updatedAt:       createdAt,
	}
	session.recordEvent(SessionCreated, "", "", time.Time{}, createdAt)
	return session, nil
}

func RestoreGameSession(snapshot SessionSnapshot) (*GameSession, error) {
	if err := validateSnapshot(snapshot); err != nil {
		return nil, err
	}
	players := append([]SessionPlayer(nil), snapshot.Players...)
	return &GameSession{
		sessionID:         snapshot.SessionID,
		houseID:           snapshot.HouseID,
		gameKey:           snapshot.GameKey,
		protocolVersion:   snapshot.ProtocolVersion,
		mode:              snapshot.Mode,
		state:             snapshot.State,
		rules:             snapshot.Rules,
		players:           players,
		createdBy:         snapshot.CreatedBy,
		createdAt:         snapshot.CreatedAt.UTC(),
		updatedAt:         snapshot.UpdatedAt.UTC(),
		readyWindowEndsAt: snapshot.ReadyWindowEndsAt.UTC(),
		countdownEndsAt:   snapshot.CountdownEndsAt.UTC(),
		startedAt:         snapshot.StartedAt.UTC(),
		endedAt:           snapshot.EndedAt.UTC(),
		endReason:         snapshot.EndReason,
		version:           snapshot.Version,
	}, nil
}

func (session *GameSession) Snapshot() SessionSnapshot {
	return SessionSnapshot{
		SessionID:         session.sessionID,
		HouseID:           session.houseID,
		GameKey:           session.gameKey,
		ProtocolVersion:   session.protocolVersion,
		Mode:              session.mode,
		State:             session.state,
		Rules:             session.rules,
		Players:           append([]SessionPlayer(nil), session.players...),
		CreatedBy:         session.createdBy,
		CreatedAt:         session.createdAt,
		UpdatedAt:         session.updatedAt,
		ReadyWindowEndsAt: session.readyWindowEndsAt,
		CountdownEndsAt:   session.countdownEndsAt,
		StartedAt:         session.startedAt,
		EndedAt:           session.endedAt,
		EndReason:         session.endReason,
		Version:           session.version,
	}
}

func (session *GameSession) State() SessionState {
	return session.state
}

func (session *GameSession) Version() int64 {
	return session.version
}

func (session *GameSession) Player(playerID string) (SessionPlayer, bool) {
	index := session.playerIndex(playerID)
	if index < 0 {
		return SessionPlayer{}, false
	}
	return session.players[index], true
}

func (session *GameSession) Players() []SessionPlayer {
	return append([]SessionPlayer(nil), session.players...)
}

func (session *GameSession) ReadyPlayerCount() int {
	count := 0
	for _, player := range session.players {
		if player.State == PlayerReady {
			count++
		}
	}
	return count
}

func (session *GameSession) JoinPlayer(playerID string, now time.Time) error {
	if playerID == "" {
		return ErrPlayerIDRequired
	}
	if err := session.requireOperationTime(now); err != nil {
		return err
	}
	if session.state != SessionLobby && session.state != SessionReadyWindow {
		return ErrJoinClosed
	}
	now = now.UTC()
	index := session.playerIndex(playerID)
	if index >= 0 && session.players[index].State != PlayerLeft {
		return ErrPlayerAlreadyJoined
	}
	if session.currentPlayerCount() >= session.rules.MaximumPlayers {
		return ErrSessionFull
	}

	if index >= 0 {
		session.players[index] = SessionPlayer{
			PlayerID: playerID,
			State:    PlayerWaiting,
			JoinedAt: now,
		}
	} else {
		session.players = append(session.players, SessionPlayer{
			PlayerID: playerID,
			State:    PlayerWaiting,
			JoinedAt: now,
		})
	}
	session.recordEvent(PlayerJoined, playerID, "", time.Time{}, now)
	return nil
}

func (session *GameSession) SetReady(playerID string, ready bool, now time.Time) error {
	if err := session.requireOperationTime(now); err != nil {
		return err
	}
	if session.state != SessionLobby && session.state != SessionReadyWindow {
		return ErrReadyClosed
	}
	index := session.playerIndex(playerID)
	if index < 0 || session.players[index].State == PlayerLeft {
		return ErrPlayerNotFound
	}
	now = now.UTC()

	if ready {
		if session.players[index].State == PlayerReady {
			return nil
		}
		if session.players[index].State != PlayerWaiting {
			return ErrReadyClosed
		}
		session.players[index].State = PlayerReady
		session.players[index].ReadyAt = now
	} else {
		if session.players[index].State == PlayerWaiting {
			return nil
		}
		if session.players[index].State != PlayerReady {
			return ErrReadyClosed
		}
		session.players[index].State = PlayerWaiting
		session.players[index].ReadyAt = time.Time{}
	}

	reason := "notReady"
	if ready {
		reason = "ready"
	}
	session.recordEvent(PlayerReadyChanged, playerID, reason, time.Time{}, now)
	session.reconcileReadyWindow(now)
	return nil
}

func (session *GameSession) LeavePlayer(playerID string, now time.Time) error {
	if err := session.requireOperationTime(now); err != nil {
		return err
	}
	if isTerminalState(session.state) {
		return ErrTerminalSession
	}
	index := session.playerIndex(playerID)
	if index < 0 {
		return ErrPlayerNotFound
	}
	if session.players[index].State == PlayerLeft {
		return nil
	}
	now = now.UTC()
	session.players[index].State = PlayerLeft
	session.players[index].LeftAt = now
	session.recordEvent(PlayerLeftSession, playerID, "", time.Time{}, now)

	switch session.state {
	case SessionReadyWindow:
		if session.ReadyPlayerCount() < session.rules.MinimumPlayers {
			session.cancelReadyWindow(now)
		}
	case SessionCountdown:
		if session.ReadyPlayerCount() < session.rules.MinimumPlayers {
			session.cancel(EndReasonInsufficientPlayers, now)
		}
	}
	return nil
}

func (session *GameSession) Advance(now time.Time) error {
	if err := session.requireOperationTime(now); err != nil {
		return err
	}
	if isTerminalState(session.state) {
		return ErrTerminalSession
	}
	session.advanceDeadlines(now.UTC())
	return nil
}

func (session *GameSession) Finish(reason string, now time.Time) error {
	if reason == "" {
		return ErrEndReasonRequired
	}
	if err := session.requireOperationTime(now); err != nil {
		return err
	}
	if session.state != SessionRunning {
		return ErrInvalidSessionState
	}
	now = now.UTC()
	for index := range session.players {
		if session.players[index].State == PlayerPlaying {
			session.players[index].State = PlayerFinished
		}
	}
	session.state = SessionFinished
	session.endedAt = now
	session.endReason = reason
	session.recordEvent(GameFinished, "", reason, time.Time{}, now)
	return nil
}

func (session *GameSession) Cancel(reason string, now time.Time) error {
	if reason == "" {
		return ErrEndReasonRequired
	}
	if err := session.requireOperationTime(now); err != nil {
		return err
	}
	if isTerminalState(session.state) {
		return ErrTerminalSession
	}
	session.cancel(reason, now.UTC())
	return nil
}

func (session *GameSession) PendingEvents() []SessionEvent {
	return append([]SessionEvent(nil), session.pendingEvents...)
}

func (session *GameSession) ClearPendingEvents() {
	session.pendingEvents = session.pendingEvents[:0]
}

func (session *GameSession) reconcileReadyWindow(now time.Time) {
	readyCount := session.ReadyPlayerCount()
	if readyCount < session.rules.MinimumPlayers {
		if session.state == SessionReadyWindow {
			session.cancelReadyWindow(now)
		}
		return
	}
	if session.state == SessionLobby {
		session.state = SessionReadyWindow
		session.readyWindowEndsAt = now.Add(session.rules.ReadyWindowDuration)
		session.recordEvent(ReadyWindowStarted, "", "", session.readyWindowEndsAt, now)
	}
	if readyCount == session.rules.MaximumPlayers || !now.Before(session.readyWindowEndsAt) {
		session.startCountdown(now)
		session.advanceDeadlines(now)
	}
}

func (session *GameSession) cancelReadyWindow(now time.Time) {
	session.state = SessionLobby
	session.readyWindowEndsAt = time.Time{}
	session.recordEvent(ReadyWindowCancelled, "", EndReasonInsufficientPlayers, time.Time{}, now)
}

func (session *GameSession) advanceDeadlines(now time.Time) {
	for {
		switch session.state {
		case SessionReadyWindow:
			if now.Before(session.readyWindowEndsAt) {
				return
			}
			transitionAt := session.readyWindowEndsAt
			if session.ReadyPlayerCount() < session.rules.MinimumPlayers {
				session.cancelReadyWindow(transitionAt)
				return
			}
			session.startCountdown(transitionAt)
		case SessionCountdown:
			if now.Before(session.countdownEndsAt) {
				return
			}
			session.startGame(session.countdownEndsAt)
		default:
			return
		}
	}
}

func (session *GameSession) startCountdown(now time.Time) {
	if session.state != SessionReadyWindow {
		return
	}
	for index := range session.players {
		if session.players[index].State != PlayerWaiting {
			continue
		}
		session.players[index].State = PlayerLeft
		session.players[index].LeftAt = now
		session.recordEvent(PlayerExcluded, session.players[index].PlayerID, "notReady", time.Time{}, now)
	}
	session.state = SessionCountdown
	session.readyWindowEndsAt = time.Time{}
	session.countdownEndsAt = now.Add(session.rules.CountdownDuration)
	session.recordEvent(CountdownStarted, "", "", session.countdownEndsAt, now)
}

func (session *GameSession) startGame(now time.Time) {
	if session.ReadyPlayerCount() < session.rules.MinimumPlayers {
		session.cancel(EndReasonInsufficientPlayers, now)
		return
	}
	for index := range session.players {
		if session.players[index].State == PlayerReady {
			session.players[index].State = PlayerPlaying
		}
	}
	session.state = SessionRunning
	session.countdownEndsAt = time.Time{}
	session.startedAt = now
	session.recordEvent(GameStarted, "", "", time.Time{}, now)
}

func (session *GameSession) cancel(reason string, now time.Time) {
	session.state = SessionCancelled
	session.readyWindowEndsAt = time.Time{}
	session.countdownEndsAt = time.Time{}
	session.endedAt = now
	session.endReason = reason
	session.recordEvent(GameCancelled, "", reason, time.Time{}, now)
}

func (session *GameSession) recordEvent(
	eventType SessionEventType,
	playerID string,
	reason string,
	deadline time.Time,
	now time.Time,
) {
	session.version++
	session.updatedAt = now
	session.pendingEvents = append(session.pendingEvents, SessionEvent{
		EventID:    uuid.NewString(),
		SessionID:  session.sessionID,
		Type:       eventType,
		Version:    session.version,
		OccurredAt: now,
		PlayerID:   playerID,
		State:      session.state,
		Deadline:   deadline,
		Reason:     reason,
	})
}

func (session *GameSession) playerIndex(playerID string) int {
	for index := range session.players {
		if session.players[index].PlayerID == playerID {
			return index
		}
	}
	return -1
}

func (session *GameSession) currentPlayerCount() int {
	count := 0
	for _, player := range session.players {
		if player.State != PlayerLeft {
			count++
		}
	}
	return count
}

func validateNewSession(params NewSessionParams) error {
	if params.SessionID == "" {
		return ErrSessionIDRequired
	}
	if params.HouseID == "" {
		return ErrHouseIDRequired
	}
	if params.GameKey == "" {
		return ErrGameKeyRequired
	}
	if params.CreatedBy == "" {
		return ErrCreatedByRequired
	}
	if params.ProtocolVersion <= 0 {
		return ErrProtocolVersion
	}
	if !isSupportedGameMode(params.Mode) {
		return ErrGameMode
	}
	if params.CreatedAt.IsZero() {
		return ErrTimestampRequired
	}
	return params.Rules.Validate()
}

func validateSnapshot(snapshot SessionSnapshot) error {
	params := NewSessionParams{
		SessionID:       snapshot.SessionID,
		HouseID:         snapshot.HouseID,
		GameKey:         snapshot.GameKey,
		ProtocolVersion: snapshot.ProtocolVersion,
		Mode:            snapshot.Mode,
		Rules:           snapshot.Rules,
		CreatedBy:       snapshot.CreatedBy,
		CreatedAt:       snapshot.CreatedAt,
	}
	if err := validateNewSession(params); err != nil {
		return errors.Join(ErrSnapshotInvalid, err)
	}
	if !isSupportedSessionState(snapshot.State) || snapshot.Version < 1 || snapshot.UpdatedAt.IsZero() {
		return ErrSnapshotInvalid
	}
	seenPlayers := make(map[string]struct{}, len(snapshot.Players))
	currentPlayers := 0
	readyPlayers := 0
	for _, player := range snapshot.Players {
		if player.PlayerID == "" || !isSupportedPlayerState(player.State) || player.JoinedAt.IsZero() {
			return ErrSnapshotInvalid
		}
		if _, exists := seenPlayers[player.PlayerID]; exists {
			return fmt.Errorf("%w: duplicate player %s", ErrSnapshotInvalid, player.PlayerID)
		}
		seenPlayers[player.PlayerID] = struct{}{}
		if player.State != PlayerLeft {
			currentPlayers++
		}
		if player.State == PlayerReady {
			readyPlayers++
		}
	}
	if currentPlayers > snapshot.Rules.MaximumPlayers || snapshot.UpdatedAt.Before(snapshot.CreatedAt) {
		return ErrSnapshotInvalid
	}
	if snapshot.State == SessionReadyWindow &&
		(snapshot.ReadyWindowEndsAt.IsZero() || readyPlayers < snapshot.Rules.MinimumPlayers) {
		return ErrSnapshotInvalid
	}
	if snapshot.State == SessionCountdown {
		if snapshot.CountdownEndsAt.IsZero() || readyPlayers < snapshot.Rules.MinimumPlayers {
			return ErrSnapshotInvalid
		}
		for _, player := range snapshot.Players {
			if player.State == PlayerWaiting || player.State == PlayerPlaying || player.State == PlayerFinished {
				return ErrSnapshotInvalid
			}
		}
	}
	if snapshot.State == SessionRunning {
		if snapshot.StartedAt.IsZero() {
			return ErrSnapshotInvalid
		}
		for _, player := range snapshot.Players {
			if player.State == PlayerWaiting || player.State == PlayerReady || player.State == PlayerFinished {
				return ErrSnapshotInvalid
			}
		}
	}
	if isTerminalState(snapshot.State) && (snapshot.EndedAt.IsZero() || snapshot.EndReason == "") {
		return ErrSnapshotInvalid
	}
	if snapshot.State == SessionFinished {
		for _, player := range snapshot.Players {
			if player.State != PlayerFinished && player.State != PlayerLeft {
				return ErrSnapshotInvalid
			}
		}
	}
	return nil
}

func requireTimestamp(now time.Time) error {
	if now.IsZero() {
		return ErrTimestampRequired
	}
	return nil
}

func (session *GameSession) requireOperationTime(now time.Time) error {
	if err := requireTimestamp(now); err != nil {
		return err
	}
	if now.Before(session.updatedAt) {
		return ErrTimestampOutOfOrder
	}
	return nil
}
