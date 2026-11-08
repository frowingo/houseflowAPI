package gamecore

import (
	"sync"
	"testing"

	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/tests/testkit/gamekit"
)

func TestGameSessionApplicationCreateAndJoinAreIdempotent(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	catalog := gamekit.SessionTestCatalog(t)
	createHandler := gameCommands.NewEnsureActiveGameSessionHandler(fixture.Repository, catalog, fixture.MembershipPolicy())
	createCommand := gameCommands.EnsureActiveGameSessionCommand{
		CommandID: "create-idempotent",
		UserID:    fixture.OwnerID,
		HouseID:   fixture.HouseID,
		GameKey:   gamekit.SessionTestGameKey,
	}
	created, err := createHandler.Handle(fixture.Ctx, createCommand)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := createHandler.Handle(fixture.Ctx, createCommand)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.SessionID != created.SessionID || replayed.Version != created.Version {
		t.Fatalf("replayed create = %s/%d, want %s/%d", replayed.SessionID, replayed.Version, created.SessionID, created.Version)
	}

	joinHandler := gameCommands.NewJoinGameSessionHandler(fixture.Repository, fixture.MembershipPolicy())
	joinCommand := gameCommands.JoinGameSessionCommand{
		CommandID: "join-idempotent",
		SessionID: created.SessionID,
		UserID:    fixture.MemberID,
	}
	start := make(chan struct{})
	results := make(chan gameDomain.SessionSnapshot, 2)
	errorsChannel := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result, err := joinHandler.Handle(fixture.Ctx, joinCommand)
			results <- result
			errorsChannel <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if result.Version != 2 {
			t.Fatalf("join result version = %d, want 2", result.Version)
		}
	}

	stored, err := fixture.Repository.FindByID(fixture.Ctx, created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Players()) != 1 || stored.Players()[0].PlayerID != fixture.MemberID {
		t.Fatalf("stored players = %+v, want member exactly once", stored.Players())
	}
	gamekit.AssertCollectionCount(t, fixture, entities.GameSessionCommandReceiptCollectionName, 2)
}

func TestGameSessionApplicationRecordsNoOpAndRejectsCommandIDReuse(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	session := fixture.CreateSession(t)
	joinHandler := gameCommands.NewJoinGameSessionHandler(fixture.Repository, fixture.MembershipPolicy())
	if _, err := joinHandler.Handle(fixture.Ctx, gameCommands.JoinGameSessionCommand{
		CommandID: "join-member",
		SessionID: session.SessionID,
		UserID:    fixture.MemberID,
	}); err != nil {
		t.Fatal(err)
	}

	readyHandler := gameCommands.NewSetPlayerReadyHandler(fixture.Repository, fixture.MembershipPolicy())
	noOpCommand := gameCommands.SetPlayerReadyCommand{
		CommandID: "not-ready-no-op",
		SessionID: session.SessionID,
		UserID:    fixture.MemberID,
		Ready:     false,
	}
	first, err := readyHandler.Handle(fixture.Ctx, noOpCommand)
	if err != nil {
		t.Fatal(err)
	}
	second, err := readyHandler.Handle(fixture.Ctx, noOpCommand)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 2 || second.Version != 2 {
		t.Fatalf("no-op versions = %d/%d, want 2/2", first.Version, second.Version)
	}
	gamekit.AssertCollectionCount(t, fixture, entities.GameSessionCommandReceiptCollectionName, 3)

	_, err = readyHandler.Handle(fixture.Ctx, gameCommands.SetPlayerReadyCommand{
		CommandID: "join-member",
		SessionID: session.SessionID,
		UserID:    fixture.MemberID,
		Ready:     true,
	})
	if !helpers.IsApplicationError(err, "game.error.command_id_reused") {
		t.Fatalf("reused command ID error = %v, want game.error.command_id_reused", err)
	}
}

func TestGameSessionApplicationEnforcesHouseAuthorization(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	session := fixture.CreateSession(t)
	queryHandler := gameQueries.NewGetGameSessionHandler(fixture.Repository, fixture.MembershipPolicy())

	if _, err := queryHandler.Handle(fixture.Ctx, gameQueries.GetGameSessionQuery{
		SessionID: session.SessionID,
		UserID:    fixture.MemberID,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := queryHandler.Handle(fixture.Ctx, gameQueries.GetGameSessionQuery{
		SessionID: session.SessionID,
		UserID:    fixture.OutsiderID,
	})
	if !helpers.IsApplicationError(err, "house.error.user_not_member") {
		t.Fatalf("outsider query error = %v, want house.error.user_not_member", err)
	}

	cancelHandler := gameCommands.NewCancelGameSessionHandler(fixture.Repository, fixture.MembershipPolicy(), gamekit.RocketsGames(), nil)
	_, err = cancelHandler.Handle(fixture.Ctx, gameCommands.CancelGameSessionCommand{
		CommandID: "member-cancel",
		SessionID: session.SessionID,
		UserID:    fixture.MemberID,
	})
	if !helpers.IsApplicationError(err, "game.error.cancel_forbidden") {
		t.Fatalf("member cancel error = %v, want game.error.cancel_forbidden", err)
	}
	cancelled, err := cancelHandler.Handle(fixture.Ctx, gameCommands.CancelGameSessionCommand{
		CommandID: "owner-cancel",
		SessionID: session.SessionID,
		UserID:    fixture.OwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != gameDomain.SessionCancelled || cancelled.EndReason != gameDomain.EndReasonCancelledByUser {
		t.Fatalf("cancelled session = %+v", cancelled)
	}
}
