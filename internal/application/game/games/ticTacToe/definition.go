// Package ticTacToe is the pilot turn-based game. It exists to prove the turn
// lane: it is added with module code and a Game document, nothing in the core.
package ticTacToe

import (
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

const (
	GameKey = "ticTacToe"
	// ProtocolVersion is the realtime envelope version the game is played on.
	ProtocolVersion     = 2
	StateSchemaVersion  = 1
	ResultSchemaVersion = 1
	MoveMessageType     = GameKey + ".move"

	Players             = 2
	ReadyWindowDuration = 30 * time.Second
	CountdownDuration   = 3 * time.Second
	DefaultTurnTimeout  = 30 * time.Second
)

type Mark string

const (
	MarkX Mark = "x"
	MarkO Mark = "o"
)

// End reasons. A user cancellation and a recovery failure use the core's.
const (
	EndThreeInARow         = "threeInARow"
	EndBoardFull           = "boardFull"
	EndTurnTimeout         = "turnTimeout"
	EndOpponentLeft        = "opponentLeft"
	EndInsufficientPlayers = "insufficientPlayers"
)

type Outcome string

const (
	OutcomeWon  Outcome = "won"
	OutcomeLost Outcome = "lost"
	OutcomeDraw Outcome = "draw"
)

func Definition() gameDomain.GameDefinition {
	return gameDomain.GameDefinition{
		GameKey:         GameKey,
		ProtocolVersion: ProtocolVersion,
		Mode:            gameDomain.TurnBasedGame,
		Rules: gameDomain.SessionRules{
			MinimumPlayers:      Players,
			MaximumPlayers:      Players,
			ReadyWindowDuration: ReadyWindowDuration,
			CountdownDuration:   CountdownDuration,
		},
	}
}

// lines are the eight ways to win.
var lines = [8][3]int{{0, 1, 2}, {3, 4, 5}, {6, 7, 8}, {0, 3, 6}, {1, 4, 7}, {2, 5, 8}, {0, 4, 8}, {2, 4, 6}}
