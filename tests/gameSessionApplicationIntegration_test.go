package tests

import (
	"sync"
	"testing"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	gameQueries "houseflowApi/internal/application/game/queries"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type gameSessionApplicationFixture struct {
	*gameSessionPersistenceFixture
	ownerID    string
	memberID   string
	outsiderID string
	houseID    string
}

func newGameSessionApplicationFixture(t *testing.T) *gameSessionApplicationFixture {
	t.Helper()
	persistence := newGameSessionPersistenceFixture(t)
	houseID := primitive.NewObjectID()
	ownerID := primitive.NewObjectID().Hex()
	memberID := primitive.NewObjectID().Hex()
	outsiderID := primitive.NewObjectID().Hex()
	now := time.Now().UTC()
	_, err := persistence.db.Collection("House").InsertOne(persistence.ctx, entities.House{
		Id:             houseID,
		OwnerId:        ownerID,
		Name:           "Game house",
		Type:           entities.SharedHouse,
		MemberIds:      []string{ownerID, memberID},
		MaxMemberCount: 4,
		CreatedOn:      now,
		UpdatedOn:      now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &gameSessionApplicationFixture{
		gameSessionPersistenceFixture: persistence,
		ownerID:                       ownerID,
		memberID:                      memberID,
		outsiderID:                    outsiderID,
		houseID:                       houseID.Hex(),
	}
}

func (fixture *gameSessionApplicationFixture) membershipPolicy() *housePolicies.MembershipPolicy {
	return housePolicies.NewMembershipPolicy(
		database.NewDbContext[entities.House](fixture.db.Client(), fixture.db.Name()),
	)
}

func (fixture *gameSessionApplicationFixture) createSession(t *testing.T) gameDomain.SessionSnapshot {
	t.Helper()
	catalog, err := gameApplication.NewDefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	handler := gameCommands.NewEnsureActiveGameSessionHandler(fixture.repository, catalog, fixture.membershipPolicy())
	snapshot, err := handler.Handle(fixture.ctx, gameCommands.EnsureActiveGameSessionCommand{
		CommandID: "create-session",
		UserID:    fixture.ownerID,
		HouseID:   fixture.houseID,
		GameKey:   gameApplication.FlappyBirdGameKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestGameSessionApplicationCreateAndJoinAreIdempotent(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	catalog, err := gameApplication.NewDefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	createHandler := gameCommands.NewEnsureActiveGameSessionHandler(fixture.repository, catalog, fixture.membershipPolicy())
	createCommand := gameCommands.EnsureActiveGameSessionCommand{
		CommandID: "create-idempotent",
		UserID:    fixture.ownerID,
		HouseID:   fixture.houseID,
		GameKey:   gameApplication.FlappyBirdGameKey,
	}
	created, err := createHandler.Handle(fixture.ctx, createCommand)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := createHandler.Handle(fixture.ctx, createCommand)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.SessionID != created.SessionID || replayed.Version != created.Version {
		t.Fatalf("replayed create = %s/%d, want %s/%d", replayed.SessionID, replayed.Version, created.SessionID, created.Version)
	}

	joinHandler := gameCommands.NewJoinGameSessionHandler(fixture.repository, fixture.membershipPolicy())
	joinCommand := gameCommands.JoinGameSessionCommand{
		CommandID: "join-idempotent",
		SessionID: created.SessionID,
		UserID:    fixture.memberID,
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
			result, err := joinHandler.Handle(fixture.ctx, joinCommand)
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

	stored, err := fixture.repository.FindByID(fixture.ctx, created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Players()) != 1 || stored.Players()[0].PlayerID != fixture.memberID {
		t.Fatalf("stored players = %+v, want member exactly once", stored.Players())
	}
	assertCollectionCount(t, fixture, entities.OutboxMessageCollectionName, 2)
	assertCollectionCount(t, fixture, entities.GameSessionCommandReceiptCollectionName, 2)
}

func TestGameSessionApplicationRecordsNoOpAndRejectsCommandIDReuse(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	session := fixture.createSession(t)
	joinHandler := gameCommands.NewJoinGameSessionHandler(fixture.repository, fixture.membershipPolicy())
	if _, err := joinHandler.Handle(fixture.ctx, gameCommands.JoinGameSessionCommand{
		CommandID: "join-member",
		SessionID: session.SessionID,
		UserID:    fixture.memberID,
	}); err != nil {
		t.Fatal(err)
	}

	readyHandler := gameCommands.NewSetPlayerReadyHandler(fixture.repository, fixture.membershipPolicy())
	noOpCommand := gameCommands.SetPlayerReadyCommand{
		CommandID: "not-ready-no-op",
		SessionID: session.SessionID,
		UserID:    fixture.memberID,
		Ready:     false,
	}
	first, err := readyHandler.Handle(fixture.ctx, noOpCommand)
	if err != nil {
		t.Fatal(err)
	}
	second, err := readyHandler.Handle(fixture.ctx, noOpCommand)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 2 || second.Version != 2 {
		t.Fatalf("no-op versions = %d/%d, want 2/2", first.Version, second.Version)
	}
	assertCollectionCount(t, fixture, entities.OutboxMessageCollectionName, 2)
	assertCollectionCount(t, fixture, entities.GameSessionCommandReceiptCollectionName, 3)

	_, err = readyHandler.Handle(fixture.ctx, gameCommands.SetPlayerReadyCommand{
		CommandID: "join-member",
		SessionID: session.SessionID,
		UserID:    fixture.memberID,
		Ready:     true,
	})
	if !helpers.IsApplicationError(err, "game.error.command_id_reused") {
		t.Fatalf("reused command ID error = %v, want game.error.command_id_reused", err)
	}
}

func TestGameSessionApplicationEnforcesHouseAuthorization(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	session := fixture.createSession(t)
	queryHandler := gameQueries.NewGetGameSessionHandler(fixture.repository, fixture.membershipPolicy())

	if _, err := queryHandler.Handle(fixture.ctx, gameQueries.GetGameSessionQuery{
		SessionID: session.SessionID,
		UserID:    fixture.memberID,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := queryHandler.Handle(fixture.ctx, gameQueries.GetGameSessionQuery{
		SessionID: session.SessionID,
		UserID:    fixture.outsiderID,
	})
	if !helpers.IsApplicationError(err, "house.error.user_not_member") {
		t.Fatalf("outsider query error = %v, want house.error.user_not_member", err)
	}

	cancelHandler := gameCommands.NewCancelGameSessionHandler(fixture.repository, fixture.membershipPolicy())
	_, err = cancelHandler.Handle(fixture.ctx, gameCommands.CancelGameSessionCommand{
		CommandID: "member-cancel",
		SessionID: session.SessionID,
		UserID:    fixture.memberID,
	})
	if !helpers.IsApplicationError(err, "game.error.cancel_forbidden") {
		t.Fatalf("member cancel error = %v, want game.error.cancel_forbidden", err)
	}
	cancelled, err := cancelHandler.Handle(fixture.ctx, gameCommands.CancelGameSessionCommand{
		CommandID: "owner-cancel",
		SessionID: session.SessionID,
		UserID:    fixture.ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != gameDomain.SessionCancelled || cancelled.EndReason != gameDomain.EndReasonCancelledByUser {
		t.Fatalf("cancelled session = %+v", cancelled)
	}
}

func assertCollectionCount(
	t *testing.T,
	fixture *gameSessionApplicationFixture,
	collectionName string,
	expected int64,
) {
	t.Helper()
	count, err := fixture.db.Collection(collectionName).CountDocuments(fixture.ctx, bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("%s count = %d, want %d", collectionName, count, expected)
	}
}
