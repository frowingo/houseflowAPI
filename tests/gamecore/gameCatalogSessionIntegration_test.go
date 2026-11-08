package gamecore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/migrations"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/middleware"
	"houseflowApi/internal/models/core"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit/gamekit"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
)

func TestDefaultGameCatalogAdvertisesOnlyEnabledGames(t *testing.T) {
	catalog, err := gamekit.BuiltinCatalog(games.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, gameKey := range []string{"flappyBird", "unknownGame"} {
		if _, err := catalog.Find(gameKey); !errors.Is(err, gameAbstract.ErrGameDefinitionNotFound) {
			t.Fatalf("%s must not resolve from the default catalog: %v", gameKey, err)
		}
	}
}

func TestEnsureActiveGameSessionIsConcurrentAndReleasesTerminalSlot(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	catalog := gamekit.SessionTestCatalog(t)
	handler := gameCommands.NewEnsureActiveGameSessionHandler(
		fixture.Repository,
		catalog,
		fixture.MembershipPolicy(),
	)

	const requestCount = 12
	start := make(chan struct{})
	results := make(chan gameDomain.SessionSnapshot, requestCount)
	errorsChannel := make(chan error, requestCount)
	var workers sync.WaitGroup
	for index := range requestCount {
		workers.Add(1)
		go func(requestIndex int) {
			defer workers.Done()
			<-start
			result, err := handler.Handle(fixture.Ctx, gameCommands.EnsureActiveGameSessionCommand{
				CommandID: fmt.Sprintf("ensure-%d", requestIndex),
				UserID:    fixture.OwnerID,
				HouseID:   fixture.HouseID,
				GameKey:   gamekit.SessionTestGameKey,
			})
			results <- result
			errorsChannel <- err
		}(index)
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
	var active gameDomain.SessionSnapshot
	for result := range results {
		if active.SessionID == "" {
			active = result
		}
		if result.SessionID != active.SessionID {
			t.Fatalf("parallel ensure returned sessions %s and %s", active.SessionID, result.SessionID)
		}
	}
	if active.Rules.MaximumPlayers != 4 {
		t.Fatalf("maximum players = %d, want house capacity 4", active.Rules.MaximumPlayers)
	}
	assertDocumentCount(t, fixture, database.GameSessionCollectionName, 1)
	assertActiveSlotCount(t, fixture, 1)

	cancelHandler := gameCommands.NewCancelGameSessionHandler(fixture.Repository, fixture.MembershipPolicy(), gamekit.RocketsGames(), nil)
	if _, err := cancelHandler.Handle(fixture.Ctx, gameCommands.CancelGameSessionCommand{
		CommandID: "cancel-active",
		SessionID: active.SessionID,
		UserID:    fixture.OwnerID,
	}); err != nil {
		t.Fatal(err)
	}
	assertActiveSlotCount(t, fixture, 0)

	replacement, err := handler.Handle(fixture.Ctx, gameCommands.EnsureActiveGameSessionCommand{
		CommandID: "ensure-replacement",
		UserID:    fixture.OwnerID,
		HouseID:   fixture.HouseID,
		GameKey:   gamekit.SessionTestGameKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.SessionID == active.SessionID {
		t.Fatal("terminal session slot must be released for a new session")
	}
	queryHandler := gameQueries.NewGetActiveGameSessionHandler(
		fixture.Repository,
		catalog,
		fixture.MembershipPolicy(),
	)
	discovered, err := queryHandler.Handle(fixture.Ctx, gameQueries.GetActiveGameSessionQuery{
		HouseID: fixture.HouseID,
		GameKey: gamekit.SessionTestGameKey,
		UserID:  fixture.MemberID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if discovered.SessionID != replacement.SessionID {
		t.Fatalf("discovered session = %s, want %s", discovered.SessionID, replacement.SessionID)
	}
}

func TestGameSessionHTTPEnsuresAndDiscoversActiveSession(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	catalog := gamekit.SessionTestCatalog(t)
	mediator := cqrs.New()
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.EnsureActiveGameSessionCommand](
		mediator,
		gameCommands.NewEnsureActiveGameSessionHandler(
			fixture.Repository,
			catalog,
			fixture.MembershipPolicy(),
		),
	)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.GetActiveGameSessionQuery](
		mediator,
		gameQueries.NewGetActiveGameSessionHandler(
			fixture.Repository,
			catalog,
			fixture.MembershipPolicy(),
		),
	)
	controller := controllers.NewGameController(mediator, nil)
	jwtService := helpers.NewJWTService("game-session-http-secret")
	token, err := jwtService.GenerateToken("owner@example.com", fixture.OwnerID, 1, "en")
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	gameRoutes := app.Group("/api/v1/game", middleware.AuthRequired(jwtService))
	gameRoutes.Put("/:gameKey/session", controller.EnsureActiveSession)
	gameRoutes.Get("/:gameKey/session", controller.GetActiveSession)

	missingRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/game/"+gamekit.SessionTestGameKey+"/session?houseId="+fixture.HouseID,
		nil,
	)
	missingRequest.Header.Set("Authorization", "Bearer "+token)
	missingResponse, err := app.Test(missingRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer missingResponse.Body.Close()
	if missingResponse.StatusCode != fiber.StatusNotFound {
		t.Fatalf("missing active session status = %d, want 404", missingResponse.StatusCode)
	}

	body, err := json.Marshal(dtos.EnsureGameSessionModel{HouseId: fixture.HouseID})
	if err != nil {
		t.Fatal(err)
	}
	ensureRequest := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/game/"+gamekit.SessionTestGameKey+"/session",
		bytes.NewReader(body),
	)
	ensureRequest.Header.Set("Authorization", "Bearer "+token)
	ensureRequest.Header.Set("Content-Type", "application/json")
	ensureResponse, err := app.Test(ensureRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer ensureResponse.Body.Close()
	if ensureResponse.StatusCode != fiber.StatusOK {
		t.Fatalf("ensure active session status = %d, want 200", ensureResponse.StatusCode)
	}
	var ensured core.ApiResponse[dtos.GameSessionResponseModel]
	if err := json.NewDecoder(ensureResponse.Body).Decode(&ensured); err != nil {
		t.Fatal(err)
	}
	if !ensured.Success || ensured.Data.SessionId == "" || ensured.Data.GameKey != gamekit.SessionTestGameKey {
		t.Fatalf("unexpected ensure response: %+v", ensured)
	}

	discoverRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/game/"+gamekit.SessionTestGameKey+"/session?houseId="+fixture.HouseID,
		nil,
	)
	discoverRequest.Header.Set("Authorization", "Bearer "+token)
	discoverResponse, err := app.Test(discoverRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer discoverResponse.Body.Close()
	if discoverResponse.StatusCode != fiber.StatusOK {
		t.Fatalf("discover active session status = %d, want 200", discoverResponse.StatusCode)
	}
	var discovered core.ApiResponse[dtos.GameSessionResponseModel]
	if err := json.NewDecoder(discoverResponse.Body).Decode(&discovered); err != nil {
		t.Fatal(err)
	}
	if discovered.Data.SessionId != ensured.Data.SessionId {
		t.Fatalf("discovered session = %s, want %s", discovered.Data.SessionId, ensured.Data.SessionId)
	}
}

// Sessions written by the previous release during the rolling deploy lack
// activeKey; retiring the outbox must keep them reachable.
func TestRetireGameOutboxBackfillsActiveSlots(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	now := time.Now().UTC()
	sessionID := uuid.NewString()
	_, err := fixture.DB.Collection(database.GameSessionCollectionName).InsertOne(fixture.Ctx, bson.M{
		"_id":             sessionID,
		"houseId":         fixture.HouseID,
		"gameKey":         gamekit.SessionTestGameKey,
		"protocolVersion": 1,
		"mode":            "realtime",
		"state":           "lobby",
		"rules": bson.M{
			"minimumPlayers":          2,
			"maximumPlayers":          4,
			"readyWindowMilliseconds": int64(30000),
			"countdownMilliseconds":   int64(3000),
		},
		"players":   bson.A{},
		"createdBy": fixture.OwnerID,
		"createdAt": now,
		"updatedAt": now,
		"version":   int64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.DB.Collection("OutboxMessage").InsertOne(fixture.Ctx, bson.M{"aggregateId": sessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.DB.Collection("_migrations").DeleteOne(fixture.Ctx, bson.M{"version": "0045"}); err != nil {
		t.Fatal(err)
	}
	if err := migrations.RunAll(fixture.Ctx, fixture.DB, migrations.AllMigrations()); err != nil {
		t.Fatal(err)
	}

	active, err := fixture.Repository.FindActive(fixture.Ctx, fixture.HouseID, gamekit.SessionTestGameKey)
	if err != nil || active.Snapshot().SessionID != sessionID {
		t.Fatalf("backfilled active session = %v", err)
	}
	collections, err := fixture.DB.ListCollectionNames(fixture.Ctx, bson.M{"name": "OutboxMessage"})
	if err != nil || len(collections) != 0 {
		t.Fatalf("outbox retained: %v %v", collections, err)
	}
}

func TestRetireFlappyBirdMigrationReleasesActiveSlots(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	sessionID := uuid.NewString()
	_, err := fixture.DB.Collection(database.GameSessionCollectionName).InsertOne(fixture.Ctx, bson.M{
		"_id":             sessionID,
		"houseId":         fixture.HouseID,
		"gameKey":         "flappyBird",
		"protocolVersion": 1,
		"mode":            "realtime",
		"state":           "countdown",
		"rules": bson.M{
			"minimumPlayers":          2,
			"maximumPlayers":          4,
			"readyWindowMilliseconds": int64(30000),
			"countdownMilliseconds":   int64(3000),
		},
		"players": bson.A{
			bson.M{"playerId": fixture.OwnerID, "state": "ready", "joinedAt": now, "readyAt": now},
			bson.M{"playerId": fixture.MemberID, "state": "ready", "joinedAt": now, "readyAt": now},
		},
		"createdBy":       fixture.OwnerID,
		"createdAt":       now,
		"updatedAt":       now,
		"countdownEndsAt": now.Add(3 * time.Second),
		"version":         int64(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.DB.Collection("_migrations").DeleteOne(fixture.Ctx, bson.M{"version": "0043"}); err != nil {
		t.Fatal(err)
	}
	if err := migrations.RunAll(fixture.Ctx, fixture.DB, migrations.AllMigrations()); err != nil {
		t.Fatal(err)
	}

	retired, err := fixture.Repository.FindByID(fixture.Ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := retired.Snapshot()
	if snapshot.State != gameDomain.SessionCancelled || snapshot.EndReason != "gameRetired" || snapshot.Version != 6 || !snapshot.CountdownEndsAt.IsZero() || snapshot.EndedAt.IsZero() {
		t.Fatalf("retired session = %+v", snapshot)
	}
	if _, err := fixture.Repository.FindActive(fixture.Ctx, fixture.HouseID, "flappyBird"); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("retired active slot lookup = %v", err)
	}
}

func assertActiveSlotCount(t *testing.T, fixture *gamekit.GameSessionApplicationFixture, want int64) {
	t.Helper()
	count, err := fixture.DB.Collection(database.GameSessionCollectionName).CountDocuments(fixture.Ctx, bson.M{"activeKey": bson.M{"$exists": true}})
	if err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("active slots = %d, want %d", count, want)
	}
}

func assertDocumentCount(
	t *testing.T,
	fixture *gamekit.GameSessionApplicationFixture,
	collectionName string,
	want int64,
) {
	t.Helper()
	count, err := fixture.DB.Collection(collectionName).CountDocuments(fixture.Ctx, bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", collectionName, count, want)
	}
}
