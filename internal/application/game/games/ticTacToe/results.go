package ticTacToe

import (
	"encoding/json"
	"errors"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/helpers"
)

var ErrInvalidResult = errors.New("tic-tac-toe result is invalid")

// completedResult ends the match with a winner, or a draw when winnerID is "".
func completedResult(session gameDomain.SessionSnapshot, current state, reason, winnerID string, now time.Time) *gameDomain.MatchResult {
	players := make([]ResultPlayerModel, 0, len(current.Players))
	for _, player := range current.Players {
		mark := player.Mark
		outcome := OutcomeDraw
		if winnerID != "" {
			outcome = OutcomeLost
			if player.PlayerID == winnerID {
				outcome = OutcomeWon
			}
		}
		players = append(players, ResultPlayerModel{PlayerID: player.PlayerID, Mark: &mark, Outcome: &outcome})
	}
	model := ResultModel{SessionID: session.SessionID, HouseID: session.HouseID, GameKey: GameKey, SchemaVersion: ResultSchemaVersion, Status: string(gameDomain.MatchCompleted),
		EndReason: reason, StartedAt: session.StartedAt.UTC(), EndedAt: now.UTC(), Players: players, Board: boardModel(current.Board), Moves: current.Moves}
	if winnerID != "" {
		model.WinnerID = &winnerID
	}
	result, _ := encodeResult(model)
	return &result
}

// cancelledResult never names a winner; marks are known once the board exists.
func cancelledResult(session gameDomain.SessionSnapshot, reason string, now time.Time) (gameDomain.MatchResult, error) {
	model := ResultModel{SessionID: session.SessionID, HouseID: session.HouseID, GameKey: GameKey, SchemaVersion: ResultSchemaVersion, Status: string(gameDomain.MatchCancelled),
		EndReason: reason, StartedAt: session.StartedAt.UTC(), EndedAt: now.UTC(), Players: []ResultPlayerModel{}}
	if current, err := decodeState(session.GameState); err == nil {
		model.Board, model.Moves = boardModel(current.Board), current.Moves
		for _, player := range current.Players {
			mark := player.Mark
			model.Players = append(model.Players, ResultPlayerModel{PlayerID: player.PlayerID, Mark: &mark})
		}
	} else {
		for _, player := range session.Players {
			if player.State == gameDomain.PlayerPlaying || player.State == gameDomain.PlayerFinished {
				model.Players = append(model.Players, ResultPlayerModel{PlayerID: player.PlayerID})
			}
		}
	}
	return encodeResult(model)
}

func encodeResult(model ResultModel) (gameDomain.MatchResult, error) {
	payload, err := json.Marshal(model)
	if err != nil {
		return gameDomain.MatchResult{}, err
	}
	return gameDomain.MatchResult{SessionID: model.SessionID, HouseID: model.HouseID, GameKey: model.GameKey, SchemaVersion: model.SchemaVersion,
		Status: gameDomain.MatchResultStatus(model.Status), EndReason: model.EndReason, StartedAt: model.StartedAt, EndedAt: model.EndedAt, Payload: payload}, nil
}

// decodeResult checks the payload against the stored metadata.
func decodeResult(stored gameDomain.MatchResult) (ResultModel, error) {
	var model ResultModel
	if err := helpers.DecodeStrictObject(stored.Payload, &model, []string{"winnerId"}, "sessionId", "houseId", "gameKey", "schemaVersion", "status", "endReason", "startedAt", "endedAt", "players", "board", "moves"); err != nil {
		return ResultModel{}, errors.Join(ErrInvalidResult, err)
	}
	sameMetadata := model.SessionID == stored.SessionID && model.HouseID == stored.HouseID && model.GameKey == GameKey && stored.GameKey == GameKey &&
		model.SchemaVersion == ResultSchemaVersion && stored.SchemaVersion == ResultSchemaVersion && model.Status == string(stored.Status) && model.EndReason == stored.EndReason &&
		sameMillisecond(model.StartedAt, stored.StartedAt) && sameMillisecond(model.EndedAt, stored.EndedAt)
	if !sameMetadata || !model.consistent() {
		return ResultModel{}, ErrInvalidResult
	}
	return model, nil
}

// consistent holds for every result this module can produce.
func (model ResultModel) consistent() bool {
	if model.Moves < 0 || model.Moves > 9 || len(model.Players) > Players {
		return false
	}
	winners := 0
	for _, player := range model.Players {
		if player.Outcome != nil && *player.Outcome == OutcomeWon {
			winners++
			if model.WinnerID == nil || *model.WinnerID != player.PlayerID {
				return false
			}
		}
	}
	switch model.Status {
	case string(gameDomain.MatchCompleted):
		return (model.WinnerID == nil) == (winners == 0) && winners <= 1 && len(model.Players) == Players
	case string(gameDomain.MatchCancelled):
		return model.WinnerID == nil && winners == 0
	default:
		return false
	}
}

// finalizeResult binds a proposed result to the stored session's identity
// and roster.
func finalizeResult(proposed gameDomain.MatchResult, session gameDomain.SessionSnapshot) (gameDomain.MatchResult, error) {
	model, err := decodeResult(proposed)
	if err != nil {
		return gameDomain.MatchResult{}, err
	}
	if model.SessionID != session.SessionID || model.HouseID != session.HouseID || session.GameKey != GameKey || session.StartedAt.IsZero() ||
		!sameMillisecond(model.StartedAt, session.StartedAt) || model.EndedAt.Before(session.UpdatedAt.Truncate(time.Millisecond)) {
		return gameDomain.MatchResult{}, ErrInvalidResult
	}
	for _, player := range model.Players {
		if _, found := sessionPlayer(session, player.PlayerID); !found {
			return gameDomain.MatchResult{}, ErrInvalidResult
		}
	}
	model.StartedAt = session.StartedAt.UTC()
	return encodeResult(model)
}

func sessionPlayer(session gameDomain.SessionSnapshot, playerID string) (gameDomain.SessionPlayer, bool) {
	for _, player := range session.Players {
		if player.PlayerID == playerID {
			return player, true
		}
	}
	return gameDomain.SessionPlayer{}, false
}

func sameMillisecond(first, second time.Time) bool {
	return first.Truncate(time.Millisecond).Equal(second.Truncate(time.Millisecond))
}
