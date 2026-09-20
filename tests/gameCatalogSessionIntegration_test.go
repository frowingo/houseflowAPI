package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/migrations"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/middleware"
	"houseflowApi/internal/models/core"
	"houseflowApi/internal/models/dtos"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
)

func TestDefaultGameCatalogOwnsFlappyBirdSessionRules(t *testing.T) {
	catalog, err := gameApplication.NewDefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := catalog.Find(gameApplication.FlappyBirdGameKey)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Mode != gameDomain.RealtimeGame || definition.ProtocolVersion != 1 {
		t.Fatalf("definition mode/version = %s/%d", definition.Mode, definition.ProtocolVersion)
	}
	if definition.Rules.MinimumPlayers != 2 ||
		definition.Rules.MaximumPlayers != 8 ||
		definition.Rules.ReadyWindowDuration != 30*time.Second ||
		definition.Rules.CountdownDuration != 3*time.Second {
		t.Fatalf("unexpected Flappy Bird rules: %+v", definition.Rules)
	}
	if _, err := catalog.Find("unknownGame"); err == nil {
		t.Fatal("unknown game must not resolve from the catalog")
	}
}

func TestEnsureActiveGameSessionIsConcurrentAndReleasesTerminalSlot(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	catalog, err := gameApplication.NewDefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	handler := gameCommands.NewEnsureActiveGameSessionHandler(
		fixture.repository,
		catalog,
		fixture.membershipPolicy(),
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
			result, err := handler.Handle(fixture.ctx, gameCommands.EnsureActiveGameSessionCommand{
				CommandID: fmt.Sprintf("ensure-%d", requestIndex),
				UserID:    fixture.ownerID,
				HouseID:   fixture.houseID,
				GameKey:   gameApplication.FlappyBirdGameKey,
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
	assertDocumentCount(t, fixture, database.ActiveGameSessionCollectionName, 1)

	cancelHandler := gameCommands.NewCancelGameSessionHandler(fixture.repository, fixture.membershipPolicy())
	if _, err := cancelHandler.Handle(fixture.ctx, gameCommands.CancelGameSessionCommand{
		CommandID: "cancel-active",
		SessionID: active.SessionID,
		UserID:    fixture.ownerID,
	}); err != nil {
		t.Fatal(err)
	}
	assertDocumentCount(t, fixture, database.ActiveGameSessionCollectionName, 0)

	replacement, err := handler.Handle(fixture.ctx, gameCommands.EnsureActiveGameSessionCommand{
		CommandID: "ensure-replacement",
		UserID:    fixture.ownerID,
		HouseID:   fixture.houseID,
		GameKey:   gameApplication.FlappyBirdGameKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.SessionID == active.SessionID {
		t.Fatal("terminal session slot must be released for a new session")
	}
	queryHandler := gameQueries.NewGetActiveGameSessionHandler(
		fixture.repository,
		catalog,
		fixture.membershipPolicy(),
	)
	discovered, err := queryHandler.Handle(fixture.ctx, gameQueries.GetActiveGameSessionQuery{
		HouseID: fixture.houseID,
		GameKey: gameApplication.FlappyBirdGameKey,
		UserID:  fixture.memberID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if discovered.SessionID != replacement.SessionID {
		t.Fatalf("discovered session = %s, want %s", discovered.SessionID, replacement.SessionID)
	}
}

func TestGameSessionHTTPEnsuresAndDiscoversActiveSession(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	catalog, err := gameApplication.NewDefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	mediator := cqrs.New()
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.EnsureActiveGameSessionCommand](
		mediator,
		gameCommands.NewEnsureActiveGameSessionHandler(
			fixture.repository,
			catalog,
			fixture.membershipPolicy(),
		),
	)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.GetActiveGameSessionQuery](
		mediator,
		gameQueries.NewGetActiveGameSessionHandler(
			fixture.repository,
			catalog,
			fixture.membershipPolicy(),
		),
	)
	controller := controllers.NewGameController(mediator, nil)
	jwtService := helpers.NewJWTService("game-session-http-secret")
	token, err := jwtService.GenerateToken("owner@example.com", fixture.ownerID, 1, "en")
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	gameRoutes := app.Group("/api/v1/game", middleware.AuthRequired(jwtService))
	gameRoutes.Put("/:gameKey/session", controller.EnsureActiveSession)
	gameRoutes.Get("/:gameKey/session", controller.GetActiveSession)

	missingRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/game/flappyBird/session?houseId="+fixture.houseID,
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

	body, err := json.Marshal(dtos.EnsureGameSessionModel{HouseId: fixture.houseID})
	if err != nil {
		t.Fatal(err)
	}
	ensureRequest := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/game/flappyBird/session",
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
	if !ensured.Success || ensured.Data.SessionId == "" || ensured.Data.GameKey != gameApplication.FlappyBirdGameKey {
		t.Fatalf("unexpected ensure response: %+v", ensured)
	}

	discoverRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/game/flappyBird/session?houseId="+fixture.houseID,
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

func TestGameCatalogMigrationBackfillsExistingActiveSession(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	now := time.Now().UTC()
	sessionID := uuid.NewString()
	_, err := fixture.db.Collection(database.GameSessionCollectionName).InsertOne(fixture.ctx, bson.M{
		"_id":             sessionID,
		"houseId":         fixture.houseID,
		"gameKey":         gameApplication.FlappyBirdGameKey,
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
		"createdBy": fixture.ownerID,
		"createdAt": now,
		"updatedAt": now,
		"version":   int64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Collection(database.ActiveGameSessionCollectionName).Drop(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Collection("_migrations").DeleteOne(fixture.ctx, bson.M{"version": "0040"}); err != nil {
		t.Fatal(err)
	}
	if err := migrations.RunAll(fixture.ctx, fixture.db, migrations.AllMigrations()); err != nil {
		t.Fatal(err)
	}

	active, err := fixture.repository.FindActive(
		fixture.ctx,
		fixture.houseID,
		gameApplication.FlappyBirdGameKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if active.Snapshot().SessionID != sessionID {
		t.Fatalf("backfilled session = %s, want %s", active.Snapshot().SessionID, sessionID)
	}
}

func assertDocumentCount(
	t *testing.T,
	fixture *gameSessionApplicationFixture,
	collectionName string,
	want int64,
) {
	t.Helper()
	count, err := fixture.db.Collection(collectionName).CountDocuments(fixture.ctx, bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", collectionName, count, want)
	}
}
