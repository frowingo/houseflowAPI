package ticTacToe

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	ticTacToe "houseflowApi/internal/application/game/games/ticTacToe"
	"houseflowApi/internal/application/game/module"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/tests/testkit"
	"houseflowApi/tests/testkit/gamekit"

	"go.mongodb.org/mongo-driver/bson"
)

// Every move is a durable command: the state commits with its receipt, a
// retried move applies once, and the winning move commits the result and
// releases the house's active slot in the same transaction.
func TestTurnGameMovesCommitWithTheirReceipt(t *testing.T) {
	fixture := gamekit.NewTurnGameFixture(t)
	session := fixture.Start(t)
	id := session.SessionID
	if _, err := fixture.Play(id, "o-first", fixture.MemberID, 4); !helpers.IsApplicationError(err, "game.error.not_your_turn") {
		t.Fatalf("O moved first: %v", err)
	}
	first, err := fixture.Play(id, "x-0", fixture.OwnerID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != session.Version+1 {
		t.Fatalf("one move = one version: %d -> %d", session.Version, first.Version)
	}
	retried, err := fixture.Play(id, "x-0", fixture.OwnerID, 0)
	if err != nil || retried.Version != first.Version {
		t.Fatalf("retried move: version %d, %v", retried.Version, err)
	}
	if _, err := fixture.Play(id, "x-0", fixture.OwnerID, 5); !helpers.IsApplicationError(err, "game.error.command_id_reused") {
		t.Fatalf("reused command ID: %v", err)
	}
	if _, err := fixture.Play(id, "o-taken", fixture.MemberID, 0); !helpers.IsApplicationError(err, "game.error.invalid_move") {
		t.Fatalf("occupied cell: %v", err)
	}
	if _, err := fixture.Play(id, "outsider", fixture.OutsiderID, 1); err == nil {
		t.Fatal("a non-member moved")
	}
	for _, move := range []struct {
		id     string
		player string
		cell   int
	}{{"o-3", fixture.MemberID, 3}, {"x-1", fixture.OwnerID, 1}, {"o-4", fixture.MemberID, 4}} {
		if _, err := fixture.Play(id, move.id, move.player, move.cell); err != nil {
			t.Fatal(err)
		}
	}
	finished, err := fixture.Play(id, "x-2", fixture.OwnerID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != gameDomain.SessionFinished || finished.EndReason != ticTacToe.EndThreeInARow || !finished.TurnDeadline.IsZero() || len(finished.GameState) == 0 {
		t.Fatalf("finished = %+v", finished)
	}
	result := fixture.Result(t, id)
	if result.WinnerID == nil || *result.WinnerID != fixture.OwnerID || result.Moves != 5 {
		t.Fatalf("result = %+v", result)
	}
	var stored bson.M
	if err := fixture.DB.Collection(database.GameMatchResultCollectionName).FindOne(fixture.Ctx, bson.M{"_id": id}).Decode(&stored); err != nil || stored["runtimeEpoch"] != finished.Version-2 {
		t.Fatalf("result fence = %v (finished version %d) %v", stored["runtimeEpoch"], finished.Version, err)
	}
	if replay, err := fixture.Play(id, "x-2", fixture.OwnerID, 2); err != nil || replay.State != gameDomain.SessionFinished {
		t.Fatalf("retried winning move: %+v %v", replay, err)
	}
	if _, err := fixture.Repository.FindActive(fixture.Ctx, fixture.HouseID, ticTacToe.GameKey); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("active slot kept: %v", err)
	}
	if _, err := fixture.Play(id, "late", fixture.MemberID, 8); err == nil {
		t.Fatal("moved after the match ended")
	}
}

// A turn that runs out, a player leaving and a user cancellation each end the
// match with a stored result.
func TestTurnGameTimeoutLeaveAndCancelEndMatches(t *testing.T) {
	fixture := gamekit.NewTurnGameFixture(t)

	session := fixture.Start(t)
	expired, err := fixture.Advance.Handle(fixture.Ctx, gameCommands.AdvanceGameSessionCommand{CommandID: fixture.CommandID("expire"), SessionID: session.SessionID, AdvanceAt: session.TurnDeadline})
	if err != nil {
		t.Fatal(err)
	}
	if result := fixture.Result(t, session.SessionID); expired.State != gameDomain.SessionFinished || result.EndReason != ticTacToe.EndTurnTimeout || *result.WinnerID != fixture.MemberID || !result.EndedAt.Equal(session.TurnDeadline.Truncate(time.Millisecond)) {
		t.Fatalf("timeout = %s %+v", expired.State, result)
	}

	session = fixture.Start(t)
	if _, err := fixture.Play(session.SessionID, fixture.CommandID("move"), fixture.OwnerID, 4); err != nil {
		t.Fatal(err)
	}
	left, err := fixture.Leave.Handle(fixture.Ctx, gameCommands.LeaveGameSessionCommand{CommandID: fixture.CommandID("leave"), SessionID: session.SessionID, UserID: fixture.OwnerID})
	if err != nil {
		t.Fatal(err)
	}
	if result := fixture.Result(t, session.SessionID); left.State != gameDomain.SessionFinished || result.EndReason != ticTacToe.EndOpponentLeft || *result.WinnerID != fixture.MemberID {
		t.Fatalf("departure = %s %+v", left.State, result)
	}

	session = fixture.Start(t)
	cancelled, err := fixture.Cancel.Handle(fixture.Ctx, gameCommands.CancelGameSessionCommand{CommandID: "cancel-turn", SessionID: session.SessionID, UserID: fixture.OwnerID})
	if err != nil {
		t.Fatal(err)
	}
	result := fixture.Result(t, session.SessionID)
	if cancelled.State != gameDomain.SessionCancelled || result.Status != string(gameDomain.MatchCancelled) || result.EndReason != gameDomain.EndReasonCancelledByUser || result.WinnerID != nil {
		t.Fatalf("cancel = %s %+v", cancelled.State, result)
	}
	if replay, err := fixture.Cancel.Handle(fixture.Ctx, gameCommands.CancelGameSessionCommand{CommandID: "cancel-turn", SessionID: session.SessionID, UserID: fixture.OwnerID}); err != nil || replay.State != gameDomain.SessionCancelled {
		t.Fatalf("retried cancel: %v", err)
	}
	query := gameQueries.NewGetMatchResultHandler(fixture.Repository, fixture.Matches, gamekit.RocketsGames(), fixture.MembershipPolicy())
	if public, err := query.Handle(fixture.Ctx, gameQueries.GetMatchResultQuery{SessionID: session.SessionID, UserID: fixture.MemberID}); err != nil || public.(ticTacToe.ResultModel).SessionID != session.SessionID {
		t.Fatalf("result over HTTP: %+v %v", public, err)
	}
}

// The pilot plays end to end over two realtime instances with the shared
// lobby, deadlines and transport; nothing in the core names the game.
func TestTicTacToeOverRealtimeGateway(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, ticTacToe.Definition().Rules)
	session := persistTicTacToeSession(t, fixture.GameSessionApplicationFixture)
	owner := fixture.Connect(t, 0, session.SessionID, fixture.OwnerID)
	member := fixture.Connect(t, 1, session.SessionID, fixture.MemberID)

	// The persisted match is running without a state; the room creates it.
	initial := awaitTicTacToeState(t, owner, func(state ticTacToe.StateModel) bool { return state.Phase == ticTacToe.PhasePlaying })
	if initial.CurrentPlayerID == nil || *initial.CurrentPlayerID != fixture.OwnerID || initial.YourMark == nil || *initial.YourMark != ticTacToe.MarkX || initial.TurnEndsAt == nil {
		t.Fatalf("initial state = %+v", initial)
	}
	member.Send(t, ticTacToe.MoveMessageType, "early", ticTacToe.MoveModel{Cell: 4})
	rejection := member.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "early"
	})
	if rejection.Error == nil || rejection.Error.Code != "game.error.not_your_turn" {
		t.Fatalf("out of turn: %+v", rejection)
	}
	member.Send(t, ticTacToe.MoveMessageType, "malformed", map[string]any{"cell": 12})
	member.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "malformed"
	})

	for index, move := range []struct {
		socket *gamekit.RocketsSocket
		cell   int
	}{{owner, 0}, {member, 3}, {owner, 1}, {member, 4}} {
		messageID := fmt.Sprintf("move-%d", index)
		move.socket.Send(t, ticTacToe.MoveMessageType, messageID, ticTacToe.MoveModel{Cell: move.cell})
		move.socket.Await(t, func(message realtime.ServerMessage) bool {
			if message.Type == realtime.CommandRejectedMessageType && message.MessageID == messageID {
				t.Fatalf("move rejected: %+v", message)
			}
			return message.Type == realtime.CommandAcceptedMessageType && message.MessageID == messageID
		})
		awaitTicTacToeState(t, owner, func(state ticTacToe.StateModel) bool { return state.Moves == index+1 })
	}
	seen := awaitTicTacToeState(t, member, func(state ticTacToe.StateModel) bool { return state.Moves == 4 })
	if seen.YourMark == nil || *seen.YourMark != ticTacToe.MarkO || seen.Board[3] == nil || *seen.Board[3] != ticTacToe.MarkO {
		t.Fatalf("member view = %+v", seen)
	}

	owner.Send(t, ticTacToe.MoveMessageType, "winning", ticTacToe.MoveModel{Cell: 2})
	for _, socket := range []*gamekit.RocketsSocket{owner, member} {
		ended := awaitTicTacToeState(t, socket, func(state ticTacToe.StateModel) bool { return state.Phase == ticTacToe.PhaseEnded })
		if ended.CurrentPlayerID != nil || ended.Moves != 5 {
			t.Fatalf("ended view = %+v", ended)
		}
		message := socket.Await(t, func(message realtime.ServerMessage) bool { return message.Type == ticTacToe.GameKey+".result" })
		var result ticTacToe.ResultModel
		if err := json.Unmarshal(message.Payload, &result); err != nil || result.WinnerID == nil || *result.WinnerID != fixture.OwnerID || result.EndReason != ticTacToe.EndThreeInARow {
			t.Fatalf("result message = %s %v", message.Payload, err)
		}
	}
}

func persistTicTacToeSession(t *testing.T, fixture *gamekit.GameSessionApplicationFixture) gameDomain.SessionSnapshot {
	t.Helper()
	now := time.Now().UTC()
	rules := ticTacToe.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 0, 0
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "tic-tac-toe-session", HouseID: fixture.HouseID, GameKey: ticTacToe.GameKey,
		ProtocolVersion: ticTacToe.ProtocolVersion, Mode: gameDomain.TurnBasedGame, Rules: rules, CreatedBy: fixture.OwnerID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fixture.OwnerID, fixture.MemberID} {
		if err := session.JoinPlayer(id, now); err != nil {
			t.Fatal(err)
		}
		if err := session.SetReady(id, true, now); err != nil {
			t.Fatal(err)
		}
	}
	if session.Snapshot().State != gameDomain.SessionRunning {
		t.Fatal("expected running session")
	}
	if _, err := fixture.Repository.Create(fixture.Ctx, session.Snapshot(), session.PendingEvents(), gamekit.PersistenceCommand("tic-tac-toe-create", "create", "ticTacToe")); err != nil {
		t.Fatal(err)
	}
	return session.Snapshot()
}

func awaitTicTacToeState(t *testing.T, socket *gamekit.RocketsSocket, predicate func(ticTacToe.StateModel) bool) ticTacToe.StateModel {
	t.Helper()
	var state ticTacToe.StateModel
	socket.Await(t, func(message realtime.ServerMessage) bool {
		if message.Type != ticTacToe.GameKey+".state" {
			return false
		}
		if err := json.Unmarshal(message.Payload, &state); err != nil {
			t.Fatal(err)
		}
		return predicate(state)
	})
	return state
}

// A turn that runs out while nobody is connected still ends: the recovery
// scanner opens the room, which reads the state back from Mongo and resolves
// the overdue turn, releasing the house's active slot.
func TestTurnGameRecoveryExpiresTurnsWithoutSockets(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	persisted := persistTicTacToeSession(t, fixture)
	session, err := fixture.Repository.FindByID(fixture.Ctx, persisted.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	started, err := ticTacToe.Module().Start(module.Turn{Session: session.Snapshot(), Now: session.Snapshot().StartedAt})
	if err != nil {
		t.Fatal(err)
	}
	version := session.Version()
	deadline := time.Now().UTC().Add(50 * time.Millisecond)
	if err := session.UpdateGameState(started.State, deadline, session.Snapshot().UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Repository.Save(fixture.Ctx, version, session.Snapshot(), session.PendingEvents(), gamekit.PersistenceCommand("orphan-state", "seed", "ticTacToe")); err != nil {
		t.Fatal(err)
	}
	coordinator := gamekit.NewTestCoordinator(t, testkit.PrepareRedis(t), "turnRecovery")
	gamekit.NewRecoveryManager(t, fixture, coordinator, realtime.RoomManagerOptions{RecoveryScanInterval: 50 * time.Millisecond})
	matches := database.NewGameMatchRepository(fixture.DB.Client(), fixture.DB.Name())
	gamekit.Eventually(t, 5*time.Second, func() bool {
		_, err := matches.FindResult(fixture.Ctx, persisted.SessionID)
		return err == nil
	})
	stored, _ := matches.FindResult(fixture.Ctx, persisted.SessionID)
	if stored.EndReason != ticTacToe.EndTurnTimeout || !stored.EndedAt.Equal(deadline.Truncate(time.Millisecond)) {
		t.Fatalf("orphaned turn = %+v", stored)
	}
	if _, err := fixture.Repository.FindActive(fixture.Ctx, fixture.HouseID, ticTacToe.GameKey); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("active slot kept: %v", err)
	}
}
