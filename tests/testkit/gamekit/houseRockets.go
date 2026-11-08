package gamekit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/infrastructure/coordination"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/realtime"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func AssertHouseRocketsJSON(t *testing.T, expected json.RawMessage, actual any) {
	t.Helper()
	encoded, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var expectedJSON, actualJSON any
	if err := json.Unmarshal(expected, &expectedJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &actualJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expectedJSON, actualJSON) {
		t.Fatalf("wire shape changed\nexpected: %s\nactual: %s", expected, encoded)
	}
}

func RoundTripHouseRocketsMessage[T any](t *testing.T, data json.RawMessage) {
	t.Helper()
	var message realtime.V2ServerMessage[T]
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatal(err)
	}
	AssertHouseRocketsJSON(t, data, realtime.NewV2ServerMessage(message.Type, message.MessageID, message.Sequence, message.SentAt, message.Payload))
}

func RunOwnerProcessHelper(t *testing.T) {
	if os.Getenv("HOUSEFLOW_RECOVERY_HELPER") != "true" {
		t.Skip("subprocess owner helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(os.Getenv("HOUSEFLOW_TEST_MONGO_URI")))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	repository := database.NewGameSessionRepository(client, os.Getenv("HOUSEFLOW_RECOVERY_DB"))
	coordinator, err := coordination.NewRedisCoordinator(coordination.RedisOptions{URL: os.Getenv("HOUSEFLOW_TEST_REDIS_URL"), InstanceID: "crashOwnerHelper"})
	if err != nil {
		t.Fatal(err)
	}
	coordinator.Start(ctx)
	mediator := cqrs.New()
	matches := database.NewGameMatchRepository(client, os.Getenv("HOUSEFLOW_RECOVERY_DB"))
	RegisterRocketsResults(mediator, repository, matches, nil)
	manager, err := realtime.NewRoomManager(coordinator, repository, mediator, realtime.RoomManagerOptions{Games: RocketsGames(), LeaseTTL: 2 * time.Second, RenewInterval: 100 * time.Millisecond, CommandTimeout: 300 * time.Millisecond, CheckpointInterval: 50 * time.Millisecond, RecoveryScanInterval: -1, MatchRepository: matches})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnsureRoom(ctx, os.Getenv("HOUSEFLOW_RECOVERY_SESSION")); err != nil {
		t.Fatal(err)
	}
	fmt.Println("ownerReady")
	<-ctx.Done()
	CloseRoomManager(t, manager)
}

func NewRecoveryManager(t *testing.T, fixture *GameSessionApplicationFixture, coordinator coordinationAbstract.Coordinator, options realtime.RoomManagerOptions) *realtime.RoomManager {
	t.Helper()
	mediator := NewGameSessionMediator(fixture)
	matches := database.NewGameMatchRepository(fixture.DB.Client(), fixture.DB.Name())
	RegisterRocketsResults(mediator, fixture.Repository, matches, nil)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.AuthorizeCancellationQuery](mediator, gameQueries.NewAuthorizeCancellationHandler(fixture.Repository, fixture.MembershipPolicy()))
	options.MatchRepository = matches
	options.Games = RocketsGames()
	if options.RecoveryScanInterval == 0 {
		options.RecoveryScanInterval = -1
	}
	manager, err := realtime.NewRoomManager(coordinator, fixture.Repository, mediator, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(fixture.Ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { CloseRoomManager(t, manager) })
	return manager
}

func PersistRocketsSession(t *testing.T, fixture *GameSessionApplicationFixture, running bool) gameDomain.SessionSnapshot {
	t.Helper()
	now := time.Now().UTC()
	rules := houseRockets.Definition().Rules
	if running {
		rules.ReadyWindowDuration = 0
		rules.CountdownDuration = 0
	}
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "rockets-runtime-session", HouseID: fixture.HouseID, GameKey: houseRockets.GameKey,
		ProtocolVersion: houseRockets.ProtocolVersion, Mode: gameDomain.RealtimeGame, Rules: rules, CreatedBy: fixture.OwnerID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if running {
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
	}
	if _, err := fixture.Repository.Create(fixture.Ctx, session.Snapshot(), session.PendingEvents(), PersistenceCommand("rockets-create", "create", "rockets")); err != nil {
		t.Fatal(err)
	}
	return session.Snapshot()
}

func NewRocketsRuntime(t *testing.T) (*houseRockets.Runtime, time.Time) {
	t.Helper()
	now := time.Now()
	runtime, err := houseRockets.NewRuntime(houseRockets.NewSimulationParams{SessionID: "runtime-test", HouseID: "house-test", StartedAt: now,
		Players: []houseRockets.PlayerIdentity{{PlayerID: "first", DisplayName: "First"}, {PlayerID: "second", DisplayName: "Second"}}}, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, now
}

func BindRocketsControl(t *testing.T, runtime *houseRockets.Runtime, id, connection string, now time.Time) houseRockets.HouseRocketsControlGrantedModel {
	t.Helper()
	if err := runtime.Submit(houseRockets.RuntimeInput{Kind: houseRockets.InputBind, PlayerID: id, ConnectionID: connection, RuntimeEpoch: 7, CreatedAt: now}, now); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Advance(now); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-runtime.Events():
		if event.Grant == nil {
			t.Fatalf("binding event: %+v", event)
		}
		return *event.Grant
	default:
		t.Fatal("missing control grant")
		return houseRockets.HouseRocketsControlGrantedModel{}
	}
}

func SteerRockets(grant houseRockets.HouseRocketsControlGrantedModel, connection string, sequence int64, heading float64, now time.Time) houseRockets.RuntimeInput {
	return houseRockets.RuntimeInput{Kind: houseRockets.InputSteer, PlayerID: grant.PlayerID, ConnectionID: connection, RuntimeEpoch: grant.RuntimeEpoch,
		ControlGeneration: grant.ControlGeneration, InputSequence: sequence, Heading: heading, CreatedAt: now}
}
