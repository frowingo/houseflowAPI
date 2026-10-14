package tests

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	ticTacToe "houseflowApi/internal/application/game/games/ticTacToe"
	"houseflowApi/internal/application/game/module"
)

// ticTacToeTurn starts a match between contract-a (X) and contract-b (O).
func ticTacToeTurn(t *testing.T) (module.TurnModule, gameDomain.SessionSnapshot) {
	t.Helper()
	game := ticTacToe.Module()
	session := runningContractSession(t, game.Definition())
	started, err := game.Start(module.Turn{Session: session, Now: session.StartedAt})
	if err != nil || started.Result != nil {
		t.Fatalf("start: %+v %v", started, err)
	}
	session.GameState, session.TurnDeadline = started.State, started.Deadline
	return game, session
}

func playTicTacToe(t *testing.T, game module.TurnModule, session gameDomain.SessionSnapshot, playerID string, cell int) (gameDomain.SessionSnapshot, module.TurnOutcome, error) {
	t.Helper()
	move, err := game.DecodeMove(ticTacToe.MoveMessageType, json.RawMessage(`{"cell":`+string(rune('0'+cell))+`}`))
	if err != nil {
		t.Fatal(err)
	}
	now := session.UpdatedAt.Add(time.Second)
	outcome, err := game.ApplyMove(module.Turn{Session: session, Now: now}, playerID, move)
	if err == nil {
		session.GameState, session.TurnDeadline, session.UpdatedAt = outcome.State, outcome.Deadline, now
	}
	return session, outcome, err
}

func ticTacToeResult(t *testing.T, game module.TurnModule, result *gameDomain.MatchResult) ticTacToe.ResultModel {
	t.Helper()
	if result == nil {
		t.Fatal("match did not end")
	}
	decoded, err := game.DecodeResult(*result)
	if err != nil {
		t.Fatal(err)
	}
	return decoded.(ticTacToe.ResultModel)
}

func TestTicTacToeRulesWinAndRejectIllegalMoves(t *testing.T) {
	game, session := ticTacToeTurn(t)
	if session.TurnDeadline.Sub(session.StartedAt) != ticTacToe.DefaultTurnTimeout {
		t.Fatalf("turn deadline = %v", session.TurnDeadline)
	}
	if _, _, err := playTicTacToe(t, game, session, "contract-b", 0); !errors.Is(err, module.ErrNotYourTurn) {
		t.Fatalf("O moved first: %v", err)
	}
	session, _, err := playTicTacToe(t, game, session, "contract-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := playTicTacToe(t, game, session, "contract-b", 0); !errors.Is(err, module.ErrInvalidInput) {
		t.Fatalf("occupied cell accepted: %v", err)
	}
	if _, _, err := playTicTacToe(t, game, session, "outsider", 1); !errors.Is(err, module.ErrPlayerNotFound) {
		t.Fatalf("outsider moved: %v", err)
	}
	for _, move := range []struct {
		player string
		cell   int
	}{{"contract-b", 3}, {"contract-a", 1}, {"contract-b", 4}} {
		if session, _, err = playTicTacToe(t, game, session, move.player, move.cell); err != nil {
			t.Fatal(err)
		}
	}
	view, err := game.View(module.Turn{Session: session, Now: session.UpdatedAt}, "contract-b")
	if err != nil {
		t.Fatal(err)
	}
	state := view.(ticTacToe.StateModel)
	if state.Phase != ticTacToe.PhasePlaying || state.CurrentPlayerID == nil || *state.CurrentPlayerID != "contract-a" || state.YourMark == nil || *state.YourMark != ticTacToe.MarkO || state.Moves != 4 || state.Board[3] == nil {
		t.Fatalf("view = %+v", state)
	}
	_, outcome, err := playTicTacToe(t, game, session, "contract-a", 2)
	if err != nil {
		t.Fatal(err)
	}
	result := ticTacToeResult(t, game, outcome.Result)
	if result.Status != string(gameDomain.MatchCompleted) || result.EndReason != ticTacToe.EndThreeInARow || result.WinnerID == nil || *result.WinnerID != "contract-a" || result.Moves != 5 {
		t.Fatalf("result = %+v", result)
	}

	for name, payload := range map[string]string{"outOfRange": `{"cell":9}`, "unknownField": `{"cell":1,"x":1}`, "missing": `{}`, "fraction": `{"cell":1.5}`} {
		if _, err := game.DecodeMove(ticTacToe.MoveMessageType, json.RawMessage(payload)); !errors.Is(err, module.ErrInvalidInput) {
			t.Errorf("%s move accepted: %v", name, err)
		}
	}
}

func TestTicTacToeDrawTimeoutAndDeparture(t *testing.T) {
	game, session := ticTacToeTurn(t)
	var outcome module.TurnOutcome
	var err error
	// X O X / X O O / O X X
	for _, move := range []struct {
		player string
		cell   int
	}{{"contract-a", 0}, {"contract-b", 1}, {"contract-a", 2}, {"contract-b", 4}, {"contract-a", 3}, {"contract-b", 5}, {"contract-a", 7}, {"contract-b", 6}, {"contract-a", 8}} {
		if session, outcome, err = playTicTacToe(t, game, session, move.player, move.cell); err != nil {
			t.Fatal(err)
		}
	}
	draw := ticTacToeResult(t, game, outcome.Result)
	if draw.EndReason != ticTacToe.EndBoardFull || draw.WinnerID != nil || *draw.Players[0].Outcome != ticTacToe.OutcomeDraw {
		t.Fatalf("draw = %+v", draw)
	}

	game, session = ticTacToeTurn(t)
	expired, err := game.Expire(module.Turn{Session: session, Now: session.TurnDeadline})
	if err != nil {
		t.Fatal(err)
	}
	if forfeit := ticTacToeResult(t, game, expired.Result); forfeit.EndReason != ticTacToe.EndTurnTimeout || *forfeit.WinnerID != "contract-b" {
		t.Fatalf("timeout = %+v", forfeit)
	}

	session.Players[1].State = gameDomain.PlayerLeft
	left, err := game.Reconcile(module.Turn{Session: session, Now: session.StartedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if departure := ticTacToeResult(t, game, left.Result); departure.EndReason != ticTacToe.EndOpponentLeft || *departure.WinnerID != "contract-a" {
		t.Fatalf("departure = %+v", departure)
	}
}

func TestTicTacToeResultRejectsTampering(t *testing.T) {
	game, session := ticTacToeTurn(t)
	expired, err := game.Expire(module.Turn{Session: session, Now: session.TurnDeadline})
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*gameDomain.MatchResult){
		"otherSession": func(result *gameDomain.MatchResult) { result.SessionID = "other" },
		"twoWinners": func(result *gameDomain.MatchResult) {
			result.Payload = []byte(replaceOnce(string(result.Payload), `"outcome":"lost"`, `"outcome":"won"`))
		},
		"unknownField": func(result *gameDomain.MatchResult) {
			result.Payload = []byte(replaceOnce(string(result.Payload), `{"sessionId"`, `{"extra":1,"sessionId"`))
		},
		"changedReason": func(result *gameDomain.MatchResult) { result.EndReason = ticTacToe.EndBoardFull },
	} {
		tampered := *expired.Result
		tampered.Payload = append(json.RawMessage(nil), tampered.Payload...)
		change(&tampered)
		if _, err := game.DecodeResult(tampered); err == nil {
			t.Errorf("%s result accepted", name)
		}
	}
}

func replaceOnce(value, old, replacement string) string {
	for index := 0; index+len(old) <= len(value); index++ {
		if value[index:index+len(old)] == old {
			return value[:index] + replacement + value[index+len(old):]
		}
	}
	return value
}
