package tests

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

	"go.mongodb.org/mongo-driver/bson"
)

type turnGameFixture struct {
	*gameSessionApplicationFixture
	matches *database.GameMatchRepository
	ensure  *gameCommands.EnsureActiveGameSessionHandler
	join    *gameCommands.JoinGameSessionHandler
	ready   *gameCommands.SetPlayerReadyHandler
	advance *gameCommands.AdvanceGameSessionHandler
	move    *gameCommands.ApplyTurnMoveHandler
	leave   *gameCommands.LeaveGameSessionHandler
	cancel  *gameCommands.CancelGameSessionHandler
	command int
}

func newTurnGameFixture(t *testing.T) *turnGameFixture {
	t.Helper()
	fixture := &turnGameFixture{gameSessionApplicationFixture: newGameSessionApplicationFixture(t)}
	fixture.matches = database.NewGameMatchRepository(fixture.db.Client(), fixture.db.Name())
	turns := gameCommands.NewTurnEngine(rocketsGames(), fixture.matches)
	definition := ticTacToe.Definition()
	definition.Rules.CountdownDuration = 50 * time.Millisecond
	catalog, err := newDefinitionCatalog(definition)
	if err != nil {
		t.Fatal(err)
	}
	policy := fixture.membershipPolicy()
	fixture.ensure = gameCommands.NewEnsureActiveGameSessionHandler(fixture.repository, catalog, policy)
	fixture.join = gameCommands.NewJoinGameSessionHandler(fixture.repository, policy)
	fixture.ready = gameCommands.NewSetPlayerReadyHandler(fixture.repository, policy)
	fixture.advance = gameCommands.NewAdvanceGameSessionHandler(fixture.repository, turns)
	fixture.move = gameCommands.NewApplyTurnMoveHandler(fixture.repository, policy, turns)
	fixture.leave = gameCommands.NewLeaveGameSessionHandler(fixture.repository, policy, turns)
	fixture.cancel = gameCommands.NewCancelGameSessionHandler(fixture.repository, policy, rocketsGames(), turns)
	return fixture
}

func (fixture *turnGameFixture) commandID(kind string) string {
	fixture.command++
	return fmt.Sprintf("%s-%d", kind, fixture.command)
}

// start plays the lobby through the domain's own deadlines: the owner plays X.
func (fixture *turnGameFixture) start(t *testing.T) gameDomain.SessionSnapshot {
	t.Helper()
	snapshot, err := fixture.ensure.Handle(fixture.ctx, gameCommands.EnsureActiveGameSessionCommand{CommandID: fixture.commandID("ensure"), UserID: fixture.ownerID, HouseID: fixture.houseID, GameKey: ticTacToe.GameKey})
	if err != nil {
		t.Fatal(err)
	}
	for _, playerID := range []string{fixture.ownerID, fixture.memberID} {
		if snapshot, err = fixture.join.Handle(fixture.ctx, gameCommands.JoinGameSessionCommand{CommandID: fixture.commandID("join"), SessionID: snapshot.SessionID, UserID: playerID}); err != nil {
			t.Fatal(err)
		}
		if snapshot, err = fixture.ready.Handle(fixture.ctx, gameCommands.SetPlayerReadyCommand{CommandID: fixture.commandID("ready"), SessionID: snapshot.SessionID, UserID: playerID, Ready: true}); err != nil {
			t.Fatal(err)
		}
	}
	if snapshot.State != gameDomain.SessionCountdown {
		t.Fatalf("lobby state = %s", snapshot.State)
	}
	// The state is created by the same command that starts the match, which
	// the room sends when the countdown ends.
	time.Sleep(time.Until(snapshot.CountdownEndsAt))
	started, err := fixture.advance.Handle(fixture.ctx, gameCommands.AdvanceGameSessionCommand{CommandID: fixture.commandID("advance"), SessionID: snapshot.SessionID, AdvanceAt: snapshot.CountdownEndsAt})
	if err != nil {
		t.Fatal(err)
	}
	if started.State != gameDomain.SessionRunning || len(started.GameState) == 0 || !started.TurnDeadline.Equal(started.StartedAt.Add(ticTacToe.DefaultTurnTimeout)) || started.Version != snapshot.Version+2 {
		t.Fatalf("started = state %s, deadline %v, version %d -> %d, game state %s", started.State, started.TurnDeadline, snapshot.Version, started.Version, started.GameState)
	}
	return started
}

func (fixture *turnGameFixture) play(sessionID, commandID, playerID string, cell int) (gameDomain.SessionSnapshot, error) {
	return fixture.move.Handle(fixture.ctx, gameCommands.ApplyTurnMoveCommand{CommandID: commandID, SessionID: sessionID, UserID: playerID, MoveType: ticTacToe.MoveMessageType, Payload: json.RawMessage(fmt.Sprintf(`{"cell":%d}`, cell))})
}

func (fixture *turnGameFixture) result(t *testing.T, sessionID string) ticTacToe.ResultModel {
	t.Helper()
	stored, err := fixture.matches.FindResult(fixture.ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ticTacToe.Module().DecodeResult(stored)
	if err != nil {
		t.Fatal(err)
	}
	return decoded.(ticTacToe.ResultModel)
}

// Every move is a durable command: the state commits with its receipt, a
// retried move applies once, and the winning move commits the result and
// releases the house's active slot in the same transaction.
func TestTurnGameMovesCommitWithTheirReceipt(t *testing.T) {
	fixture := newTurnGameFixture(t)
	session := fixture.start(t)
	id := session.SessionID
	if _, err := fixture.play(id, "o-first", fixture.memberID, 4); !helpers.IsApplicationError(err, "game.error.not_your_turn") {
		t.Fatalf("O moved first: %v", err)
	}
	first, err := fixture.play(id, "x-0", fixture.ownerID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != session.Version+1 {
		t.Fatalf("one move = one version: %d -> %d", session.Version, first.Version)
	}
	retried, err := fixture.play(id, "x-0", fixture.ownerID, 0)
	if err != nil || retried.Version != first.Version {
		t.Fatalf("retried move: version %d, %v", retried.Version, err)
	}
	if _, err := fixture.play(id, "x-0", fixture.ownerID, 5); !helpers.IsApplicationError(err, "game.error.command_id_reused") {
		t.Fatalf("reused command ID: %v", err)
	}
	if _, err := fixture.play(id, "o-taken", fixture.memberID, 0); !helpers.IsApplicationError(err, "game.error.invalid_move") {
		t.Fatalf("occupied cell: %v", err)
	}
	if _, err := fixture.play(id, "outsider", fixture.outsiderID, 1); err == nil {
		t.Fatal("a non-member moved")
	}
	for _, move := range []struct {
		id     string
		player string
		cell   int
	}{{"o-3", fixture.memberID, 3}, {"x-1", fixture.ownerID, 1}, {"o-4", fixture.memberID, 4}} {
		if _, err := fixture.play(id, move.id, move.player, move.cell); err != nil {
			t.Fatal(err)
		}
	}
	finished, err := fixture.play(id, "x-2", fixture.ownerID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != gameDomain.SessionFinished || finished.EndReason != ticTacToe.EndThreeInARow || !finished.TurnDeadline.IsZero() || len(finished.GameState) == 0 {
		t.Fatalf("finished = %+v", finished)
	}
	result := fixture.result(t, id)
	if result.WinnerID == nil || *result.WinnerID != fixture.ownerID || result.Moves != 5 {
		t.Fatalf("result = %+v", result)
	}
	var stored bson.M
	if err := fixture.db.Collection(database.GameMatchResultCollectionName).FindOne(fixture.ctx, bson.M{"_id": id}).Decode(&stored); err != nil || stored["runtimeEpoch"] != finished.Version-2 {
		t.Fatalf("result fence = %v (finished version %d) %v", stored["runtimeEpoch"], finished.Version, err)
	}
	if replay, err := fixture.play(id, "x-2", fixture.ownerID, 2); err != nil || replay.State != gameDomain.SessionFinished {
		t.Fatalf("retried winning move: %+v %v", replay, err)
	}
	if _, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, ticTacToe.GameKey); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("active slot kept: %v", err)
	}
	if _, err := fixture.play(id, "late", fixture.memberID, 8); err == nil {
		t.Fatal("moved after the match ended")
	}
}

// A turn that runs out, a player leaving and a user cancellation each end the
// match with a stored result.
func TestTurnGameTimeoutLeaveAndCancelEndMatches(t *testing.T) {
	fixture := newTurnGameFixture(t)

	session := fixture.start(t)
	expired, err := fixture.advance.Handle(fixture.ctx, gameCommands.AdvanceGameSessionCommand{CommandID: fixture.commandID("expire"), SessionID: session.SessionID, AdvanceAt: session.TurnDeadline})
	if err != nil {
		t.Fatal(err)
	}
	if result := fixture.result(t, session.SessionID); expired.State != gameDomain.SessionFinished || result.EndReason != ticTacToe.EndTurnTimeout || *result.WinnerID != fixture.memberID || !result.EndedAt.Equal(session.TurnDeadline.Truncate(time.Millisecond)) {
		t.Fatalf("timeout = %s %+v", expired.State, result)
	}

	session = fixture.start(t)
	if _, err := fixture.play(session.SessionID, fixture.commandID("move"), fixture.ownerID, 4); err != nil {
		t.Fatal(err)
	}
	left, err := fixture.leave.Handle(fixture.ctx, gameCommands.LeaveGameSessionCommand{CommandID: fixture.commandID("leave"), SessionID: session.SessionID, UserID: fixture.ownerID})
	if err != nil {
		t.Fatal(err)
	}
	if result := fixture.result(t, session.SessionID); left.State != gameDomain.SessionFinished || result.EndReason != ticTacToe.EndOpponentLeft || *result.WinnerID != fixture.memberID {
		t.Fatalf("departure = %s %+v", left.State, result)
	}

	session = fixture.start(t)
	cancelled, err := fixture.cancel.Handle(fixture.ctx, gameCommands.CancelGameSessionCommand{CommandID: "cancel-turn", SessionID: session.SessionID, UserID: fixture.ownerID})
	if err != nil {
		t.Fatal(err)
	}
	result := fixture.result(t, session.SessionID)
	if cancelled.State != gameDomain.SessionCancelled || result.Status != string(gameDomain.MatchCancelled) || result.EndReason != gameDomain.EndReasonCancelledByUser || result.WinnerID != nil {
		t.Fatalf("cancel = %s %+v", cancelled.State, result)
	}
	if replay, err := fixture.cancel.Handle(fixture.ctx, gameCommands.CancelGameSessionCommand{CommandID: "cancel-turn", SessionID: session.SessionID, UserID: fixture.ownerID}); err != nil || replay.State != gameDomain.SessionCancelled {
		t.Fatalf("retried cancel: %v", err)
	}
	query := gameQueries.NewGetMatchResultHandler(fixture.repository, fixture.matches, rocketsGames(), fixture.membershipPolicy())
	if public, err := query.Handle(fixture.ctx, gameQueries.GetMatchResultQuery{SessionID: session.SessionID, UserID: fixture.memberID}); err != nil || public.(ticTacToe.ResultModel).SessionID != session.SessionID {
		t.Fatalf("result over HTTP: %+v %v", public, err)
	}
}

// The pilot plays end to end over two realtime instances with the shared
// lobby, deadlines and transport; nothing in the core names the game.
func TestTicTacToeOverRealtimeGateway(t *testing.T) {
	fixture := newRocketsGatewayFixture(t, ticTacToe.Definition().Rules)
	session := persistTicTacToeSession(t, fixture.gameSessionApplicationFixture)
	owner := fixture.connect(t, 0, session.SessionID, fixture.ownerID)
	member := fixture.connect(t, 1, session.SessionID, fixture.memberID)

	// The persisted match is running without a state; the room creates it.
	initial := awaitTicTacToeState(t, owner, func(state ticTacToe.StateModel) bool { return state.Phase == ticTacToe.PhasePlaying })
	if initial.CurrentPlayerID == nil || *initial.CurrentPlayerID != fixture.ownerID || initial.YourMark == nil || *initial.YourMark != ticTacToe.MarkX || initial.TurnEndsAt == nil {
		t.Fatalf("initial state = %+v", initial)
	}
	member.send(t, ticTacToe.MoveMessageType, "early", ticTacToe.MoveModel{Cell: 4})
	rejection := member.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "early"
	})
	if rejection.Error == nil || rejection.Error.Code != "game.error.not_your_turn" {
		t.Fatalf("out of turn: %+v", rejection)
	}
	member.send(t, ticTacToe.MoveMessageType, "malformed", map[string]any{"cell": 12})
	member.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "malformed"
	})

	for index, move := range []struct {
		socket *rocketsSocket
		cell   int
	}{{owner, 0}, {member, 3}, {owner, 1}, {member, 4}} {
		messageID := fmt.Sprintf("move-%d", index)
		move.socket.send(t, ticTacToe.MoveMessageType, messageID, ticTacToe.MoveModel{Cell: move.cell})
		move.socket.await(t, func(message realtime.ServerMessage) bool {
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

	owner.send(t, ticTacToe.MoveMessageType, "winning", ticTacToe.MoveModel{Cell: 2})
	for _, socket := range []*rocketsSocket{owner, member} {
		ended := awaitTicTacToeState(t, socket, func(state ticTacToe.StateModel) bool { return state.Phase == ticTacToe.PhaseEnded })
		if ended.CurrentPlayerID != nil || ended.Moves != 5 {
			t.Fatalf("ended view = %+v", ended)
		}
		message := socket.await(t, func(message realtime.ServerMessage) bool { return message.Type == ticTacToe.GameKey+".result" })
		var result ticTacToe.ResultModel
		if err := json.Unmarshal(message.Payload, &result); err != nil || result.WinnerID == nil || *result.WinnerID != fixture.ownerID || result.EndReason != ticTacToe.EndThreeInARow {
			t.Fatalf("result message = %s %v", message.Payload, err)
		}
	}
}

func persistTicTacToeSession(t *testing.T, fixture *gameSessionApplicationFixture) gameDomain.SessionSnapshot {
	t.Helper()
	now := time.Now().UTC()
	rules := ticTacToe.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 0, 0
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "tic-tac-toe-session", HouseID: fixture.houseID, GameKey: ticTacToe.GameKey,
		ProtocolVersion: ticTacToe.ProtocolVersion, Mode: gameDomain.TurnBasedGame, Rules: rules, CreatedBy: fixture.ownerID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fixture.ownerID, fixture.memberID} {
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
	if _, err := fixture.repository.Create(fixture.ctx, session.Snapshot(), session.PendingEvents(), persistenceCommand("tic-tac-toe-create", "create", "ticTacToe")); err != nil {
		t.Fatal(err)
	}
	return session.Snapshot()
}

func awaitTicTacToeState(t *testing.T, socket *rocketsSocket, predicate func(ticTacToe.StateModel) bool) ticTacToe.StateModel {
	t.Helper()
	var state ticTacToe.StateModel
	socket.await(t, func(message realtime.ServerMessage) bool {
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
	fixture := newGameSessionApplicationFixture(t)
	persisted := persistTicTacToeSession(t, fixture)
	session, err := fixture.repository.FindByID(fixture.ctx, persisted.SessionID)
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
	if _, err := fixture.repository.Save(fixture.ctx, version, session.Snapshot(), session.PendingEvents(), persistenceCommand("orphan-state", "seed", "ticTacToe")); err != nil {
		t.Fatal(err)
	}
	coordinator := newTestCoordinator(t, prepareRedis(t), "turnRecovery")
	newRecoveryManager(t, fixture, coordinator, realtime.RoomManagerOptions{RecoveryScanInterval: 50 * time.Millisecond})
	matches := database.NewGameMatchRepository(fixture.db.Client(), fixture.db.Name())
	eventually(t, 5*time.Second, func() bool {
		_, err := matches.FindResult(fixture.ctx, persisted.SessionID)
		return err == nil
	})
	stored, _ := matches.FindResult(fixture.ctx, persisted.SessionID)
	if stored.EndReason != ticTacToe.EndTurnTimeout || !stored.EndedAt.Equal(deadline.Truncate(time.Millisecond)) {
		t.Fatalf("orphaned turn = %+v", stored)
	}
	if _, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, ticTacToe.GameKey); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("active slot kept: %v", err)
	}
}
