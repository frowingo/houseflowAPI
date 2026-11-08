package gamekit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games"
	"houseflowApi/internal/application/game/games/fausSwitch"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/application/game/module"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/middleware"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/core"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit"

	fasthttpWebsocket "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// A race without flips ends by itself in about eight seconds of race time;
// the wait covers that, the countdown and a slow CI machine.
const SwitchRaceTimeout = 40 * time.Second

// housePlayers returns count house members in join order: the owner, the
// member and new members named like the fixture's users.
func (fixture *RocketsGatewayFixture) HousePlayers(t *testing.T, count int) []string {
	t.Helper()
	players := []string{fixture.OwnerID, fixture.MemberID}
	var added []string
	for index := len(players); index < count; index++ {
		id := primitive.NewObjectID()
		_, err := fixture.DB.Collection("User").InsertOne(fixture.Ctx, entities.User{Id: id, Firstname: fmt.Sprintf("Player%d", index+1), Lastname: "House",
			Email: id.Hex() + "@test.dev", IsActive: true, HouseIds: []string{fixture.HouseID}})
		if err != nil {
			t.Fatal(err)
		}
		added = append(added, id.Hex())
	}
	if len(added) > 0 {
		houseID, _ := primitive.ObjectIDFromHex(fixture.HouseID)
		_, err := fixture.DB.Collection("House").UpdateOne(fixture.Ctx, bson.M{"_id": houseID},
			bson.M{"$push": bson.M{"memberIds": bson.M{"$each": added}}, "$set": bson.M{"maxMemberCount": fausSwitch.MaximumPlayers}})
		if err != nil {
			t.Fatal(err)
		}
	}
	return append(players, added...)[:count]
}

func (socket *RocketsSocket) SwitchSnapshot(t *testing.T, predicate func(fausSwitch.FausSwitchSnapshotModel) bool) fausSwitch.FausSwitchSnapshotModel {
	t.Helper()
	return socket.SwitchSnapshotWithin(t, 6*time.Second, predicate)
}

func (socket *RocketsSocket) SwitchSnapshotWithin(t *testing.T, timeout time.Duration, predicate func(fausSwitch.FausSwitchSnapshotModel) bool) fausSwitch.FausSwitchSnapshotModel {
	t.Helper()
	var snapshot fausSwitch.FausSwitchSnapshotModel
	socket.awaitWithin(t, timeout, func(message realtime.ServerMessage) bool {
		if message.Type != fausSwitch.SnapshotMessageType {
			return false
		}
		snapshot = fausSwitch.FausSwitchSnapshotModel{}
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		return predicate(snapshot)
	})
	return snapshot
}

// liveSwitchSnapshot skips the snapshots already queued and waits for one
// that satisfies the predicate, so a flip is sent for the tick a player sees
// now, inside the rewind window.
func (socket *RocketsSocket) LiveSwitchSnapshot(t *testing.T, predicate func(fausSwitch.FausSwitchSnapshotModel) bool) fausSwitch.FausSwitchSnapshotModel {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for queued := true; queued; {
			select {
			case <-socket.Messages:
			default:
				queued = false
			}
		}
		if snapshot := socket.SwitchSnapshot(t, func(fausSwitch.FausSwitchSnapshotModel) bool { return true }); predicate(snapshot) {
			return snapshot
		}
	}
	t.Fatal("no live snapshot matched")
	return fausSwitch.FausSwitchSnapshotModel{}
}

// switchGrant waits for a grant newer than the given epoch.
func (socket *RocketsSocket) SwitchGrant(t *testing.T, afterEpoch int64) fausSwitch.FausSwitchControlGrantedModel {
	t.Helper()
	var grant fausSwitch.FausSwitchControlGrantedModel
	socket.Await(t, func(message realtime.ServerMessage) bool {
		if message.Type != fausSwitch.ControlGrantedMessageType {
			return false
		}
		if err := json.Unmarshal(message.Payload, &grant); err != nil {
			t.Fatal(err)
		}
		return grant.RuntimeEpoch > afterEpoch
	})
	return grant
}

func (socket *RocketsSocket) Accepted(t *testing.T, messageID string) {
	t.Helper()
	socket.Await(t, func(message realtime.ServerMessage) bool {
		if message.Type == realtime.CommandRejectedMessageType && message.MessageID == messageID {
			t.Fatalf("%s rejected: %+v", messageID, message.Error)
		}
		return message.Type == realtime.CommandAcceptedMessageType && message.MessageID == messageID
	})
}

// sessionColors waits for a session snapshot whose lobby colors are want.
func (socket *RocketsSocket) SessionColors(t *testing.T, want map[string]fausSwitch.Color) {
	t.Helper()
	socket.Await(t, func(message realtime.ServerMessage) bool {
		var session dtos.GameSessionResponseModel
		if message.Type != realtime.GameSessionSnapshotEventType || json.Unmarshal(message.Payload, &session) != nil {
			return false
		}
		colors := ColorsOf(t, session.LobbyState)
		if len(colors) != len(want) {
			return false
		}
		for id, color := range want {
			if colors[id] != color {
				return false
			}
		}
		return true
	})
}

func (socket *RocketsSocket) Flip(t *testing.T, messageID, generation string, sequence, clientTick int64) {
	t.Helper()
	socket.Send(t, fausSwitch.FlipMessageType, messageID, fausSwitch.FausSwitchFlipModel{ControlGeneration: generation, InputSequence: sequence, ClientTick: clientTick})
}

// switchResult waits for the committed result message and checks it is the
// one the API serves, byte for byte.
func (fixture *RocketsGatewayFixture) SwitchResult(t *testing.T, socket *RocketsSocket, sessionID, userID string) fausSwitch.FausSwitchResultModel {
	t.Helper()
	message := socket.awaitWithin(t, SwitchRaceTimeout, func(message realtime.ServerMessage) bool { return message.Type == fausSwitch.ResultMessageType })
	var result fausSwitch.FausSwitchResultModel
	if err := json.Unmarshal(message.Payload, &result); err != nil {
		t.Fatal(err)
	}
	for index := range fixture.URLs {
		status, read := fixture.TanksResultHTTP(t, index, sessionID, userID)
		if status != 200 || !bytes.Equal(message.Payload, read) {
			t.Fatalf("HTTP/socket result mismatch on instance %d: %d %s", index, status, read)
		}
	}
	return result
}

type RocketsGatewayFixture struct {
	*GameSessionApplicationFixture
	JWT      *helpers.JWTService
	URLs     []string
	Services []*realtime.Service
	gameKey  string
}

func NewRocketsGatewayFixture(t *testing.T, rules gameDomain.SessionRules, enabledOptions ...bool) *RocketsGatewayFixture {
	return NewConfiguredRocketsGatewayFixture(t, rules, 0, 0, enabledOptions...)
}

func NewConfiguredRocketsGatewayFixture(t *testing.T, rules gameDomain.SessionRules, maxRooms, maxConnections int, enabledOptions ...bool) *RocketsGatewayFixture {
	t.Helper()
	definition := houseRockets.Definition()
	definition.Rules = rules
	return NewGameGatewayFixture(t, definition, maxRooms, maxConnections, enabledOptions...)
}

// newGameGatewayFixture runs two gateway instances whose catalog serves the
// definition; every built-in tick game has a realtime module.
func NewGameGatewayFixture(t *testing.T, definition gameDomain.GameDefinition, maxRooms, maxConnections int, enabledOptions ...bool) *RocketsGatewayFixture {
	t.Helper()
	enabled := len(enabledOptions) == 0 || enabledOptions[0]
	// A disabled game keeps its catalog entry but has no realtime module.
	fixtureGames, err := games.NewRegistry(games.Options{Realtime: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return NewGameGatewayFixtureWith(t, definition, fixtureGames, maxRooms, maxConnections)
}

// newGameGatewayFixtureWith runs the gateways with the given game modules.
func NewGameGatewayFixtureWith(t *testing.T, definition gameDomain.GameDefinition, fixtureGames *module.Registry, maxRooms, maxConnections int) *RocketsGatewayFixture {
	t.Helper()
	fixture := &RocketsGatewayFixture{GameSessionApplicationFixture: NewGameSessionApplicationFixture(t), JWT: helpers.NewJWTService("rockets-gateway-secret"), gameKey: definition.GameKey}
	for index, id := range []string{fixture.OwnerID, fixture.MemberID, fixture.OutsiderID} {
		objectID, _ := primitive.ObjectIDFromHex(id)
		_, err := fixture.DB.Collection("User").InsertOne(fixture.Ctx, entities.User{Id: objectID, Firstname: fmt.Sprintf("Player%d", index), Lastname: "House", Email: id + "@test.dev", IsActive: true, HouseIds: []string{}})
		if err != nil {
			t.Fatal(err)
		}
	}
	redisURL := testkit.PrepareRedis(t)
	catalog, err := NewDefinitionCatalog(definition)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		mediator := NewGameSessionMediator(fixture.GameSessionApplicationFixture)
		matches := database.NewGameMatchRepository(fixture.DB.Client(), fixture.DB.Name())
		RegisterRocketsResults(mediator, fixture.Repository, matches, fixture.MembershipPolicy())
		cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.ApplyLobbyIntentCommand](mediator, gameCommands.NewApplyLobbyIntentHandler(fixture.Repository, fixture.MembershipPolicy(), fixtureGames))
		cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.AuthorizeCancellationQuery](mediator, gameQueries.NewAuthorizeCancellationHandler(fixture.Repository, fixture.MembershipPolicy()))
		cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.EnsureActiveGameSessionCommand](mediator, gameCommands.NewEnsureActiveGameSessionHandler(fixture.Repository, catalog, fixture.MembershipPolicy()))
		cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.GetActiveGameSessionQuery](mediator, gameQueries.NewGetActiveGameSessionHandler(fixture.Repository, catalog, fixture.MembershipPolicy()))
		coordinator := NewTestCoordinator(t, redisURL, fmt.Sprintf("rockets-gateway-%d", index))
		service, err := realtime.NewService(coordinator, fixture.Repository, mediator,
			realtime.RoomManagerOptions{Games: fixtureGames, LeaseTTL: 10 * time.Second, RenewInterval: 500 * time.Millisecond, CommandTimeout: 2 * time.Second, ReconcileInterval: 100 * time.Millisecond, ParticipantDirectory: database.NewGameParticipantDirectory(fixture.DB.Client(), fixture.DB.Name()), MatchRepository: matches, MaxOwnedRooms: maxRooms},
			realtime.GatewayOptions{AllowedOrigins: []string{"*"}, PingInterval: 100 * time.Millisecond, IdleTimeout: 3 * time.Second, WriteTimeout: time.Second, PresenceTTL: time.Second, PresenceRefresh: 200 * time.Millisecond, MaxConnections: maxConnections})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Start(fixture.Ctx); err != nil {
			t.Fatal(err)
		}
		app := fiber.New(fiber.Config{DisableStartupMessage: true})
		routes := app.Group("/api/v1/game", middleware.AuthRequired(fixture.JWT))
		controller := controllers.NewGameController(mediator, nil)
		routes.Put("/:gameKey/session", controller.EnsureActiveSession)
		routes.Get("/:gameKey/session", controller.GetActiveSession)
		routes.Get("/:sessionId/result", controller.GetResult)
		routes.Get("/:sessionId/realtime", service.UpgradeMiddleware(), service.Handler())
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = app.Listener(listener) }()
		fixture.URLs = append(fixture.URLs, "http://"+listener.Addr().String())
		fixture.Services = append(fixture.Services, service)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = service.Close(ctx)
			_ = app.ShutdownWithContext(ctx)
		})
	}
	return fixture
}

func (fixture *RocketsGatewayFixture) EnsureHTTP(t *testing.T) dtos.GameSessionResponseModel {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPut, fixture.URLs[0]+"/api/v1/game/"+fixture.gameKey+"/session", bytes.NewBufferString(fmt.Sprintf(`{"houseId":%q}`, fixture.HouseID)))
	request.Header.Set("Authorization", "Bearer "+GenerateGatewayToken(t, fixture.JWT, fixture.OwnerID))
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body core.ApiResponse[dtos.GameSessionResponseModel]
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !body.Success || body.Data.ProtocolVersion != 2 {
		t.Fatalf("create HTTP: %d %+v", response.StatusCode, body)
	}
	request, _ = http.NewRequest(http.MethodGet, fixture.URLs[1]+"/api/v1/game/"+fixture.gameKey+"/session?houseId="+fixture.HouseID, nil)
	request.Header.Set("Authorization", "Bearer "+GenerateGatewayToken(t, fixture.JWT, fixture.MemberID))
	response2, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response2.Body.Close()
	var discovered core.ApiResponse[dtos.GameSessionResponseModel]
	if err := json.NewDecoder(response2.Body).Decode(&discovered); err != nil {
		t.Fatal(err)
	}
	if response2.StatusCode != 200 || discovered.Data.SessionId != body.Data.SessionId {
		t.Fatalf("discovery: %+v", discovered)
	}
	return body.Data
}

type RocketsSocket struct {
	Connection *fasthttpWebsocket.Conn
	Mutex      sync.Mutex
	Messages   chan realtime.ServerMessage
	Errors     chan error
}

func (fixture *RocketsGatewayFixture) Connect(t *testing.T, index int, sessionID, userID string) *RocketsSocket {
	t.Helper()
	endpoint := "ws" + fixture.URLs[index][4:] + "/api/v1/game/" + sessionID + "/realtime?protocolVersion=2"
	connection := DialGateway(t, endpoint, GenerateGatewayToken(t, fixture.JWT, userID))
	socket := &RocketsSocket{Connection: connection, Messages: make(chan realtime.ServerMessage, 1024), Errors: make(chan error, 1)}
	t.Cleanup(func() { _ = connection.Close() })
	go func() {
		for {
			var message realtime.ServerMessage
			if err := connection.ReadJSON(&message); err != nil {
				socket.Errors <- err
				return
			}
			select {
			case socket.Messages <- message:
			default:
				socket.Errors <- fmt.Errorf("test reader queue full")
				return
			}
		}
	}()
	welcome := socket.Await(t, func(message realtime.ServerMessage) bool { return true })
	if welcome.ProtocolVersion != 2 || welcome.Type != realtime.WelcomeMessageType {
		t.Fatal("wrong protocol")
	}
	initial := socket.Await(t, func(message realtime.ServerMessage) bool { return true })
	if initial.Type != realtime.GameSessionSnapshotEventType {
		t.Fatalf("initial order: %+v", initial)
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(initial.Payload, &payload) != nil || payload["sessionId"] == nil || payload["SessionID"] != nil {
		t.Fatalf("non-camelCase session: %s", initial.Payload)
	}
	return socket
}

func (socket *RocketsSocket) Send(t *testing.T, kind, messageID string, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	socket.Mutex.Lock()
	err = socket.Connection.WriteJSON(realtime.ClientMessage{ProtocolVersion: 2, MessageID: messageID, Type: kind, Payload: raw})
	socket.Mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func (socket *RocketsSocket) Await(t *testing.T, predicate func(realtime.ServerMessage) bool) realtime.ServerMessage {
	t.Helper()
	return socket.awaitWithin(t, 6*time.Second, predicate)
}

// awaitWithin is await with a longer wait, for a whole match.
func (socket *RocketsSocket) awaitWithin(t *testing.T, timeout time.Duration, predicate func(realtime.ServerMessage) bool) realtime.ServerMessage {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case message := <-socket.Messages:
			if predicate(message) {
				return message
			}
		case err := <-socket.Errors:
			t.Fatalf("socket: %v", err)
		case <-timer.C:
			t.Fatal("timed out awaiting socket message")
		}
	}
}

func (socket *RocketsSocket) Lifecycle(t *testing.T, kind, id string, payload any) {
	t.Helper()
	socket.Send(t, kind, id, payload)
	socket.Await(t, func(message realtime.ServerMessage) bool {
		if message.Type == realtime.CommandRejectedMessageType && message.MessageID == id {
			t.Fatalf("lifecycle rejected: %+v", message)
		}
		return message.Type == realtime.GameSessionSnapshotEventType && message.MessageID == id
	})
}

func (socket *RocketsSocket) Snapshot(t *testing.T, predicate func(houseRockets.HouseRocketsSnapshotModel) bool) houseRockets.HouseRocketsSnapshotModel {
	t.Helper()
	var snapshot houseRockets.HouseRocketsSnapshotModel
	socket.Await(t, func(message realtime.ServerMessage) bool {
		if message.Type != houseRockets.SnapshotMessageType {
			return false
		}
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		return predicate(snapshot)
	})
	return snapshot
}

func (socket *RocketsSocket) Grant(t *testing.T) houseRockets.HouseRocketsControlGrantedModel {
	t.Helper()
	message := socket.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == houseRockets.ControlGrantedMessageType
	})
	var grant houseRockets.HouseRocketsControlGrantedModel
	if err := json.Unmarshal(message.Payload, &grant); err != nil {
		t.Fatal(err)
	}
	return grant
}

func GenerateGatewayToken(t *testing.T, service *helpers.JWTService, userID string) string {
	t.Helper()
	token, err := service.GenerateToken(userID+"@houseflow.test", userID, 0, "eng")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func DialGateway(t *testing.T, endpoint string, token string) *fasthttpWebsocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	connection, response, err := fasthttpWebsocket.DefaultDialer.Dial(endpoint, header)
	if err != nil {
		t.Fatalf("dial realtime gateway: %v status=%v", err, ResponseStatus(response))
	}
	return connection
}

func ResponseStatus(response *http.Response) any {
	if response == nil {
		return nil
	}
	return response.StatusCode
}
