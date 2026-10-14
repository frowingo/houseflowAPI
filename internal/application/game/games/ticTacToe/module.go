package ticTacToe

import (
	"encoding/json"
	"errors"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
)

// Module is the pilot turn-based game.
func Module() module.TurnModule { return gameModule{} }

type gameModule struct{}

func (gameModule) Definition() gameDomain.GameDefinition { return Definition() }

func (gameModule) ResolveConfig(override module.Config) (module.Config, error) {
	if !validRules(override) {
		return module.Config{}, ErrInvalidSettings
	}
	settings, err := resolveSettings(override.Settings)
	if err != nil {
		return module.Config{}, err
	}
	encoded, err := json.Marshal(settings)
	return module.Config{Rules: override.Rules, Settings: encoded}, err
}

func (gameModule) ClientSettings(settings json.RawMessage) any {
	return SettingsModel{TurnTimeoutMilliseconds: sessionSettings(settings).TurnTimeoutMilliseconds}
}

func (game gameModule) Welcome(sessionID, connectionID string, settings json.RawMessage) any {
	return WelcomeModel{SessionID: sessionID, GameKey: GameKey, ConnectionID: connectionID, ProtocolVersion: ProtocolVersion, Settings: game.ClientSettings(settings).(SettingsModel)}
}

func (gameModule) Start(turn module.Turn) (module.TurnOutcome, error) {
	current := newState(turn.Session)
	encoded, err := current.encode()
	if err != nil {
		return module.TurnOutcome{}, err
	}
	if len(current.Players) != Players {
		result, err := cancelledResult(turn.Session, EndInsufficientPlayers, turn.Now)
		return module.TurnOutcome{State: encoded, Result: &result}, err
	}
	return module.TurnOutcome{State: encoded, Deadline: nextDeadline(turn)}, nil
}

func (gameModule) DecodeMove(messageType string, payload json.RawMessage) (json.RawMessage, error) {
	if messageType != MoveMessageType {
		return nil, module.ErrUnsupportedIntent
	}
	var move MoveModel
	if err := helpers.DecodeStrictObject(payload, &move, nil, "cell"); err != nil {
		return nil, errors.Join(module.ErrInvalidInput, err)
	}
	if move.Cell < 0 || move.Cell > 8 {
		return nil, module.ErrInvalidInput
	}
	return json.Marshal(move)
}

func (gameModule) ApplyMove(turn module.Turn, playerID string, payload json.RawMessage) (module.TurnOutcome, error) {
	current, err := decodeState(turn.Session.GameState)
	if err != nil {
		return module.TurnOutcome{}, err
	}
	player, seated := current.player(playerID)
	if !seated {
		return module.TurnOutcome{}, module.ErrPlayerNotFound
	}
	if current.Players[current.Turn].PlayerID != playerID {
		return module.TurnOutcome{}, module.ErrNotYourTurn
	}
	var move MoveModel
	if err := json.Unmarshal(payload, &move); err != nil || move.Cell < 0 || move.Cell > 8 || current.Board[move.Cell] != "" {
		return module.TurnOutcome{}, module.ErrInvalidInput
	}
	current.Board[move.Cell] = player.Mark
	current.Moves++
	current.Turn = 1 - current.Turn
	encoded, err := current.encode()
	if err != nil {
		return module.TurnOutcome{}, err
	}
	if mark, won := current.winner(); won {
		return module.TurnOutcome{State: encoded, Result: completedResult(turn.Session, current, EndThreeInARow, current.markOwner(mark), turn.Now)}, nil
	}
	if current.Moves == len(current.Board) {
		return module.TurnOutcome{State: encoded, Result: completedResult(turn.Session, current, EndBoardFull, "", turn.Now)}, nil
	}
	return module.TurnOutcome{State: encoded, Deadline: nextDeadline(turn)}, nil
}

// Expire forfeits the match of the player who let the turn run out.
func (gameModule) Expire(turn module.Turn) (module.TurnOutcome, error) {
	current, err := decodeState(turn.Session.GameState)
	if err != nil {
		return module.TurnOutcome{}, err
	}
	late := current.Players[current.Turn].PlayerID
	return module.TurnOutcome{State: turn.Session.GameState, Result: completedResult(turn.Session, current, EndTurnTimeout, current.other(late).PlayerID, turn.Now)}, nil
}

// Reconcile awards the match to the player who stayed.
func (gameModule) Reconcile(turn module.Turn) (module.TurnOutcome, error) {
	current, err := decodeState(turn.Session.GameState)
	if err != nil {
		return module.TurnOutcome{}, err
	}
	var staying []string
	for _, player := range current.Players {
		if seat, found := sessionPlayer(turn.Session, player.PlayerID); found && seat.State == gameDomain.PlayerPlaying {
			staying = append(staying, player.PlayerID)
		}
	}
	switch len(staying) {
	case Players:
		return module.TurnOutcome{State: turn.Session.GameState, Deadline: turn.Session.TurnDeadline}, nil
	case 1:
		return module.TurnOutcome{State: turn.Session.GameState, Result: completedResult(turn.Session, current, EndOpponentLeft, staying[0], turn.Now)}, nil
	default:
		result, err := cancelledResult(turn.Session, EndInsufficientPlayers, turn.Now)
		return module.TurnOutcome{State: turn.Session.GameState, Result: &result}, err
	}
}

func (gameModule) View(turn module.Turn, playerID string) (any, error) {
	current, err := decodeState(turn.Session.GameState)
	if err != nil {
		return nil, err
	}
	view := StateModel{SessionID: turn.Session.SessionID, GameKey: GameKey, Phase: PhaseEnded, Board: boardModel(current.Board), Players: make([]PlayerModel, 0, len(current.Players)), Moves: current.Moves}
	for _, player := range current.Players {
		view.Players = append(view.Players, PlayerModel{PlayerID: player.PlayerID, Mark: player.Mark})
		if player.PlayerID == playerID {
			mark := player.Mark
			view.YourMark = &mark
		}
	}
	if turn.Session.State == gameDomain.SessionRunning {
		view.Phase = PhasePlaying
		current := current.Players[current.Turn].PlayerID
		view.CurrentPlayerID = &current
		if !turn.Session.TurnDeadline.IsZero() {
			endsAt := turn.Session.TurnDeadline.UTC()
			view.TurnEndsAt = &endsAt
		}
	}
	return view, nil
}

func (gameModule) CancellationResult(session gameDomain.SessionSnapshot, reason string, now time.Time) (gameDomain.MatchResult, error) {
	return cancelledResult(session, reason, now)
}

func (gameModule) FinalizeResult(proposed gameDomain.MatchResult, session gameDomain.SessionSnapshot) (gameDomain.MatchResult, error) {
	return finalizeResult(proposed, session)
}

func (gameModule) DecodeResult(stored gameDomain.MatchResult) (any, error) {
	return decodeResult(stored)
}

func nextDeadline(turn module.Turn) time.Time {
	return turn.Now.Add(sessionSettings(turn.Session.Settings).turnTimeout()).UTC()
}

var _ module.TurnModule = gameModule{}
