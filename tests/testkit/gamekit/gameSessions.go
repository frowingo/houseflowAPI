package gamekit

import (
	"context"
	"os"
	"testing"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/tests/testkit"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

type GameSessionApplicationFixture struct {
	*GameSessionPersistenceFixture
	OwnerID    string
	MemberID   string
	OutsiderID string
	HouseID    string
}

func NewGameSessionApplicationFixture(t *testing.T) *GameSessionApplicationFixture {
	t.Helper()
	persistence := NewGameSessionPersistenceFixture(t)
	houseID := primitive.NewObjectID()
	ownerID := primitive.NewObjectID().Hex()
	memberID := primitive.NewObjectID().Hex()
	outsiderID := primitive.NewObjectID().Hex()
	now := time.Now().UTC()
	_, err := persistence.DB.Collection("House").InsertOne(persistence.Ctx, entities.House{
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
	return &GameSessionApplicationFixture{
		GameSessionPersistenceFixture: persistence,
		OwnerID:                       ownerID,
		MemberID:                      memberID,
		OutsiderID:                    outsiderID,
		HouseID:                       houseID.Hex(),
	}
}

func (fixture *GameSessionApplicationFixture) MembershipPolicy() *housePolicies.MembershipPolicy {
	return housePolicies.NewMembershipPolicy(
		database.NewDbContext[entities.House](fixture.DB.Client(), fixture.DB.Name()),
	)
}

// sessionTestGameKey exercises the game-agnostic session layer without a runtime.
const SessionTestGameKey = "sessionTestGame"

func SessionTestCatalog(t *testing.T) DefinitionCatalog {
	t.Helper()
	catalog, err := NewDefinitionCatalog(gameDomain.GameDefinition{
		GameKey:         SessionTestGameKey,
		ProtocolVersion: 1,
		Mode:            gameDomain.RealtimeGame,
		Rules: gameDomain.SessionRules{
			MinimumPlayers:      2,
			MaximumPlayers:      8,
			ReadyWindowDuration: 30 * time.Second,
			CountdownDuration:   3 * time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func (fixture *GameSessionApplicationFixture) CreateSession(t *testing.T) gameDomain.SessionSnapshot {
	t.Helper()
	catalog := SessionTestCatalog(t)
	handler := gameCommands.NewEnsureActiveGameSessionHandler(fixture.Repository, catalog, fixture.MembershipPolicy())
	snapshot, err := handler.Handle(fixture.Ctx, gameCommands.EnsureActiveGameSessionCommand{
		CommandID: "create-session",
		UserID:    fixture.OwnerID,
		HouseID:   fixture.HouseID,
		GameKey:   SessionTestGameKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func AssertCollectionCount(
	t *testing.T,
	fixture *GameSessionApplicationFixture,
	collectionName string,
	expected int64,
) {
	t.Helper()
	count, err := fixture.DB.Collection(collectionName).CountDocuments(fixture.Ctx, bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("%s count = %d, want %d", collectionName, count, expected)
	}
}

type GameSessionPersistenceFixture struct {
	Ctx        context.Context
	DB         *mongo.Database
	Repository gameAbstract.GameSessionRepository
}

func NewGameSessionPersistenceFixture(t *testing.T) *GameSessionPersistenceFixture {
	t.Helper()
	uri := os.Getenv("HOUSEFLOW_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("HOUSEFLOW_TEST_MONGO_URI is required for game session persistence integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	client, err := mongo.Connect(ctx, options.Client().
		ApplyURI(uri).
		SetServerSelectionTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.Disconnect(cleanupCtx); err != nil {
			t.Errorf("disconnect test client: %v", err)
		}
	})
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		t.Fatal(err)
	}
	// A migrated database from the package's pool, reset after the test.
	db := testkit.AcquireMigratedDatabase(t, client)

	return &GameSessionPersistenceFixture{
		Ctx:        ctx,
		DB:         db,
		Repository: database.NewGameSessionRepository(client, db.Name()),
	}
}

func PersistenceCommand(commandID string, commandType string, payloadHash string) gameAbstract.CommandDescriptor {
	return gameAbstract.CommandDescriptor{
		CommandID:   commandID,
		ActorID:     "user-1",
		CommandType: commandType,
		PayloadHash: payloadHash,
	}
}
