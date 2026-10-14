package ticTacToe

import (
	"encoding/json"
	"errors"
	"sort"

	gameDomain "houseflowApi/internal/application/game/domain"
)

var ErrInvalidState = errors.New("tic-tac-toe state is invalid")

type statePlayer struct {
	PlayerID string `json:"playerId"`
	Mark     Mark   `json:"mark"`
}

// state is what the session stores between moves. Moves themselves are not
// kept; the board is the whole history this game needs.
type state struct {
	SchemaVersion int           `json:"schemaVersion"`
	Players       []statePlayer `json:"players"`
	Board         [9]Mark       `json:"board"`
	Turn          int           `json:"turn"`
	Moves         int           `json:"moves"`
}

// newState seats the session's playing players by join order; the first
// plays X and moves first.
func newState(session gameDomain.SessionSnapshot) state {
	playing := make([]gameDomain.SessionPlayer, 0, Players)
	for _, player := range session.Players {
		if player.State == gameDomain.PlayerPlaying {
			playing = append(playing, player)
		}
	}
	sort.SliceStable(playing, func(i, j int) bool {
		if playing[i].JoinedAt.Equal(playing[j].JoinedAt) {
			return playing[i].PlayerID < playing[j].PlayerID
		}
		return playing[i].JoinedAt.Before(playing[j].JoinedAt)
	})
	current := state{SchemaVersion: StateSchemaVersion, Players: make([]statePlayer, 0, len(playing))}
	for index, player := range playing {
		mark := MarkX
		if index == 1 {
			mark = MarkO
		}
		current.Players = append(current.Players, statePlayer{PlayerID: player.PlayerID, Mark: mark})
	}
	return current
}

func decodeState(raw json.RawMessage) (state, error) {
	var current state
	if err := json.Unmarshal(raw, &current); err != nil {
		return state{}, errors.Join(ErrInvalidState, err)
	}
	if !current.valid() {
		return state{}, ErrInvalidState
	}
	return current, nil
}

func (current state) valid() bool {
	if current.SchemaVersion != StateSchemaVersion || len(current.Players) != Players || current.Turn < 0 || current.Turn >= Players ||
		current.Players[0].Mark != MarkX || current.Players[1].Mark != MarkO || current.Players[0].PlayerID == "" || current.Players[0].PlayerID == current.Players[1].PlayerID {
		return false
	}
	placed := 0
	for _, cell := range current.Board {
		switch cell {
		case "":
		case MarkX, MarkO:
			placed++
		default:
			return false
		}
	}
	return placed == current.Moves
}

func (current state) encode() (json.RawMessage, error) { return json.Marshal(current) }

func (current state) player(playerID string) (statePlayer, bool) {
	for _, player := range current.Players {
		if player.PlayerID == playerID {
			return player, true
		}
	}
	return statePlayer{}, false
}

func (current state) other(playerID string) statePlayer {
	if current.Players[0].PlayerID == playerID {
		return current.Players[1]
	}
	return current.Players[0]
}

// winner returns the mark completing a line, if any.
func (current state) winner() (Mark, bool) {
	for _, line := range lines {
		first := current.Board[line[0]]
		if first != "" && first == current.Board[line[1]] && first == current.Board[line[2]] {
			return first, true
		}
	}
	return "", false
}

func (current state) markOwner(mark Mark) string {
	for _, player := range current.Players {
		if player.Mark == mark {
			return player.PlayerID
		}
	}
	return ""
}
