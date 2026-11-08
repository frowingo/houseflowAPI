package gamekit

import (
	"context"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/data/database"
	infrastructureCoordination "houseflowApi/internal/infrastructure/coordination"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/realtime"
)

func NewTestRoomManager(
	t *testing.T,
	fixture *GameSessionApplicationFixture,
	coordinator *infrastructureCoordination.RedisCoordinator,
) *realtime.RoomManager {
	t.Helper()
	mediator := NewGameSessionMediator(fixture)
	manager, err := realtime.NewRoomManager(coordinator, fixture.Repository, mediator, realtime.RoomManagerOptions{
		Games:          RocketsGames(),
		LeaseTTL:       5 * time.Second,
		RenewInterval:  500 * time.Millisecond,
		CommandTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(fixture.Ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { CloseRoomManager(t, manager) })
	return manager
}

func NewGameSessionMediator(fixture *GameSessionApplicationFixture) *cqrs.Mediator {
	mediator := cqrs.New()
	// The turn engine leaves every other game untouched.
	turns := gameCommands.NewTurnEngine(RocketsGames(), database.NewGameMatchRepository(fixture.DB.Client(), fixture.DB.Name()))
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.ReconcileGameSessionPlayersCommand](mediator, gameCommands.NewReconcileGameSessionPlayersHandler(fixture.Repository, turns))
	membershipPolicy := fixture.MembershipPolicy()
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.ApplyTurnMoveCommand](mediator, gameCommands.NewApplyTurnMoveHandler(fixture.Repository, membershipPolicy, turns))
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.JoinGameSessionCommand](
		mediator,
		gameCommands.NewJoinGameSessionHandler(fixture.Repository, membershipPolicy),
	)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.SetPlayerReadyCommand](
		mediator,
		gameCommands.NewSetPlayerReadyHandler(fixture.Repository, membershipPolicy),
	)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.LeaveGameSessionCommand](
		mediator,
		gameCommands.NewLeaveGameSessionHandler(fixture.Repository, membershipPolicy, turns),
	)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.CancelGameSessionCommand](
		mediator,
		gameCommands.NewCancelGameSessionHandler(fixture.Repository, membershipPolicy, RocketsGames(), turns),
	)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.AdvanceGameSessionCommand](
		mediator,
		gameCommands.NewAdvanceGameSessionHandler(fixture.Repository, turns),
	)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.GetGameSessionQuery](
		mediator,
		gameQueries.NewGetGameSessionHandler(fixture.Repository, membershipPolicy),
	)
	return mediator
}

func CloseRoomManager(t *testing.T, manager *realtime.RoomManager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Close(ctx); err != nil {
		t.Errorf("close room manager: %v", err)
	}
}
