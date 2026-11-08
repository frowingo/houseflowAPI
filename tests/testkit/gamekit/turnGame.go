package gamekit

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	ticTacToe "houseflowApi/internal/application/game/games/ticTacToe"
	"houseflowApi/internal/data/database"
)

type TurnGameFixture struct {
	*GameSessionApplicationFixture
	Matches *database.GameMatchRepository
	Ensure  *gameCommands.EnsureActiveGameSessionHandler
	Join    *gameCommands.JoinGameSessionHandler
	Ready   *gameCommands.SetPlayerReadyHandler
	Advance *gameCommands.AdvanceGameSessionHandler
	move    *gameCommands.ApplyTurnMoveHandler
	Leave   *gameCommands.LeaveGameSessionHandler
	Cancel  *gameCommands.CancelGameSessionHandler
	command int
}

func NewTurnGameFixture(t *testing.T) *TurnGameFixture {
	t.Helper()
	fixture := &TurnGameFixture{GameSessionApplicationFixture: NewGameSessionApplicationFixture(t)}
	fixture.Matches = database.NewGameMatchRepository(fixture.DB.Client(), fixture.DB.Name())
	turns := gameCommands.NewTurnEngine(RocketsGames(), fixture.Matches)
	definition := ticTacToe.Definition()
	definition.Rules.CountdownDuration = 50 * time.Millisecond
	catalog, err := NewDefinitionCatalog(definition)
	if err != nil {
		t.Fatal(err)
	}
	policy := fixture.MembershipPolicy()
	fixture.Ensure = gameCommands.NewEnsureActiveGameSessionHandler(fixture.Repository, catalog, policy)
	fixture.Join = gameCommands.NewJoinGameSessionHandler(fixture.Repository, policy)
	fixture.Ready = gameCommands.NewSetPlayerReadyHandler(fixture.Repository, policy)
	fixture.Advance = gameCommands.NewAdvanceGameSessionHandler(fixture.Repository, turns)
	fixture.move = gameCommands.NewApplyTurnMoveHandler(fixture.Repository, policy, turns)
	fixture.Leave = gameCommands.NewLeaveGameSessionHandler(fixture.Repository, policy, turns)
	fixture.Cancel = gameCommands.NewCancelGameSessionHandler(fixture.Repository, policy, RocketsGames(), turns)
	return fixture
}

func (fixture *TurnGameFixture) CommandID(kind string) string {
	fixture.command++
	return fmt.Sprintf("%s-%d", kind, fixture.command)
}

// start plays the lobby through the domain's own deadlines: the owner plays X.
func (fixture *TurnGameFixture) Start(t *testing.T) gameDomain.SessionSnapshot {
	t.Helper()
	snapshot, err := fixture.Ensure.Handle(fixture.Ctx, gameCommands.EnsureActiveGameSessionCommand{CommandID: fixture.CommandID("ensure"), UserID: fixture.OwnerID, HouseID: fixture.HouseID, GameKey: ticTacToe.GameKey})
	if err != nil {
		t.Fatal(err)
	}
	for _, playerID := range []string{fixture.OwnerID, fixture.MemberID} {
		if snapshot, err = fixture.Join.Handle(fixture.Ctx, gameCommands.JoinGameSessionCommand{CommandID: fixture.CommandID("join"), SessionID: snapshot.SessionID, UserID: playerID}); err != nil {
			t.Fatal(err)
		}
		if snapshot, err = fixture.Ready.Handle(fixture.Ctx, gameCommands.SetPlayerReadyCommand{CommandID: fixture.CommandID("ready"), SessionID: snapshot.SessionID, UserID: playerID, Ready: true}); err != nil {
			t.Fatal(err)
		}
	}
	if snapshot.State != gameDomain.SessionCountdown {
		t.Fatalf("lobby state = %s", snapshot.State)
	}
	// The state is created by the same command that starts the match, which
	// the room sends when the countdown ends.
	time.Sleep(time.Until(snapshot.CountdownEndsAt))
	started, err := fixture.Advance.Handle(fixture.Ctx, gameCommands.AdvanceGameSessionCommand{CommandID: fixture.CommandID("advance"), SessionID: snapshot.SessionID, AdvanceAt: snapshot.CountdownEndsAt})
	if err != nil {
		t.Fatal(err)
	}
	if started.State != gameDomain.SessionRunning || len(started.GameState) == 0 || !started.TurnDeadline.Equal(started.StartedAt.Add(ticTacToe.DefaultTurnTimeout)) || started.Version != snapshot.Version+2 {
		t.Fatalf("started = state %s, deadline %v, version %d -> %d, game state %s", started.State, started.TurnDeadline, snapshot.Version, started.Version, started.GameState)
	}
	return started
}

func (fixture *TurnGameFixture) Play(sessionID, commandID, playerID string, cell int) (gameDomain.SessionSnapshot, error) {
	return fixture.move.Handle(fixture.Ctx, gameCommands.ApplyTurnMoveCommand{CommandID: commandID, SessionID: sessionID, UserID: playerID, MoveType: ticTacToe.MoveMessageType, Payload: json.RawMessage(fmt.Sprintf(`{"cell":%d}`, cell))})
}

func (fixture *TurnGameFixture) Result(t *testing.T, sessionID string) ticTacToe.ResultModel {
	t.Helper()
	stored, err := fixture.Matches.FindResult(fixture.Ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ticTacToe.Module().DecodeResult(stored)
	if err != nil {
		t.Fatal(err)
	}
	return decoded.(ticTacToe.ResultModel)
}
