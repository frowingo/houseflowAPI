package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
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

	ws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type rocketsGatewayFixture struct {
	*gameSessionApplicationFixture
	jwt  *helpers.JWTService
	urls []string
}

func newRocketsGatewayFixture(t *testing.T, rules gameDomain.SessionRules, enabledOptions ...bool) *rocketsGatewayFixture {
	t.Helper()
	fixture := &rocketsGatewayFixture{gameSessionApplicationFixture: newGameSessionApplicationFixture(t), jwt: helpers.NewJWTService("rockets-gateway-secret")}
	enabled := len(enabledOptions) == 0 || enabledOptions[0]
	for index, id := range []string{fixture.ownerID, fixture.memberID, fixture.outsiderID} {
		objectID, _ := primitive.ObjectIDFromHex(id)
		_, err := fixture.db.Collection("User").InsertOne(fixture.ctx, entities.User{Id: objectID, Firstname: fmt.Sprintf("Player%d", index), Lastname: "House", Email: id + "@test.dev", IsActive: true, HouseIds: []string{}})
		if err != nil {
			t.Fatal(err)
		}
	}
	redisURL := prepareRedis(t)
	definition := houseRockets.Definition()
	definition.Rules = rules
	catalog, err := gameApplication.NewCatalog(definition)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		mediator := newGameSessionMediator(fixture.gameSessionApplicationFixture)
		cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.EnsureActiveGameSessionCommand](mediator, gameCommands.NewEnsureActiveGameSessionHandler(fixture.repository, catalog, fixture.membershipPolicy()))
		cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.GetActiveGameSessionQuery](mediator, gameQueries.NewGetActiveGameSessionHandler(fixture.repository, catalog, fixture.membershipPolicy()))
		coordinator := newTestCoordinator(t, redisURL, fmt.Sprintf("rockets-gateway-%d", index))
		service, err := realtime.NewService(coordinator, fixture.repository, mediator,
			realtime.RoomManagerOptions{LeaseTTL: 10 * time.Second, RenewInterval: 500 * time.Millisecond, CommandTimeout: 2 * time.Second, ReconcileInterval: 100 * time.Millisecond, ParticipantDirectory: database.NewGameParticipantDirectory(fixture.db.Client(), fixture.db.Name())},
			realtime.GatewayOptions{EnableHouseRockets: enabled, AllowedOrigins: []string{"*"}, PingInterval: 100 * time.Millisecond, IdleTimeout: 3 * time.Second, WriteTimeout: time.Second, PresenceTTL: time.Second, PresenceRefresh: 200 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Start(fixture.ctx); err != nil {
			t.Fatal(err)
		}
		app := fiber.New(fiber.Config{DisableStartupMessage: true})
		routes := app.Group("/api/v1/game", middleware.AuthRequired(fixture.jwt))
		controller := controllers.NewGameController(mediator, nil)
		routes.Put("/:gameKey/session", controller.EnsureActiveSession)
		routes.Get("/:gameKey/session", controller.GetActiveSession)
		routes.Get("/:sessionId/realtime", service.UpgradeMiddleware(), service.Handler())
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = app.Listener(listener) }()
		fixture.urls = append(fixture.urls, "http://"+listener.Addr().String())
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = service.Close(ctx)
			_ = app.ShutdownWithContext(ctx)
		})
	}
	return fixture
}

func (fixture *rocketsGatewayFixture) ensureHTTP(t *testing.T) dtos.GameSessionResponseModel {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPut, fixture.urls[0]+"/api/v1/game/houseRockets/session", bytes.NewBufferString(fmt.Sprintf(`{"houseId":%q}`, fixture.houseID)))
	request.Header.Set("Authorization", "Bearer "+generateGatewayToken(t, fixture.jwt, fixture.ownerID))
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
	request, _ = http.NewRequest(http.MethodGet, fixture.urls[1]+"/api/v1/game/houseRockets/session?houseId="+fixture.houseID, nil)
	request.Header.Set("Authorization", "Bearer "+generateGatewayToken(t, fixture.jwt, fixture.memberID))
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

type rocketsSocket struct {
	connection *ws.Conn
	mutex      sync.Mutex
	messages   chan realtime.ServerMessage
	errors     chan error
}

func (fixture *rocketsGatewayFixture) connect(t *testing.T, index int, sessionID, userID string) *rocketsSocket {
	t.Helper()
	endpoint := "ws" + fixture.urls[index][4:] + "/api/v1/game/" + sessionID + "/realtime?protocolVersion=2"
	connection := dialGateway(t, endpoint, generateGatewayToken(t, fixture.jwt, userID))
	socket := &rocketsSocket{connection: connection, messages: make(chan realtime.ServerMessage, 256), errors: make(chan error, 1)}
	t.Cleanup(func() { _ = connection.Close() })
	go func() {
		for {
			var message realtime.ServerMessage
			if err := connection.ReadJSON(&message); err != nil {
				socket.errors <- err
				return
			}
			select {
			case socket.messages <- message:
			default:
				socket.errors <- fmt.Errorf("test reader queue full")
				return
			}
		}
	}()
	welcome := socket.await(t, func(message realtime.ServerMessage) bool { return true })
	if welcome.ProtocolVersion != 2 || welcome.Type != realtime.WelcomeMessageType {
		t.Fatal("wrong protocol")
	}
	initial := socket.await(t, func(message realtime.ServerMessage) bool { return true })
	if initial.Type != realtime.GameSessionSnapshotEventType {
		t.Fatalf("initial order: %+v", initial)
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(initial.Payload, &payload) != nil || payload["sessionId"] == nil || payload["SessionID"] != nil {
		t.Fatalf("non-camelCase session: %s", initial.Payload)
	}
	return socket
}

func (socket *rocketsSocket) send(t *testing.T, kind, messageID string, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	socket.mutex.Lock()
	err = socket.connection.WriteJSON(realtime.ClientMessage{ProtocolVersion: 2, MessageID: messageID, Type: kind, Payload: raw})
	socket.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func (socket *rocketsSocket) await(t *testing.T, predicate func(realtime.ServerMessage) bool) realtime.ServerMessage {
	t.Helper()
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	for {
		select {
		case message := <-socket.messages:
			if predicate(message) {
				return message
			}
		case err := <-socket.errors:
			t.Fatalf("socket: %v", err)
		case <-timer.C:
			t.Fatal("timed out awaiting socket message")
		}
	}
}

func (socket *rocketsSocket) lifecycle(t *testing.T, kind, id string, payload any) {
	t.Helper()
	socket.send(t, kind, id, payload)
	socket.await(t, func(message realtime.ServerMessage) bool {
		if message.Type == realtime.CommandRejectedMessageType && message.MessageID == id {
			t.Fatalf("lifecycle rejected: %+v", message)
		}
		return message.Type == realtime.GameSessionSnapshotEventType && message.MessageID == id
	})
}

func (socket *rocketsSocket) snapshot(t *testing.T, predicate func(houseRockets.HouseRocketsSnapshotModel) bool) houseRockets.HouseRocketsSnapshotModel {
	t.Helper()
	var snapshot houseRockets.HouseRocketsSnapshotModel
	socket.await(t, func(message realtime.ServerMessage) bool {
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

func (socket *rocketsSocket) grant(t *testing.T) houseRockets.HouseRocketsControlGrantedModel {
	t.Helper()
	message := socket.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == houseRockets.ControlGrantedMessageType
	})
	var grant houseRockets.HouseRocketsControlGrantedModel
	if err := json.Unmarshal(message.Payload, &grant); err != nil {
		t.Fatal(err)
	}
	return grant
}

func TestHouseRocketsGatewayTwoInstancesHTTPCountdownSteeringAndForfeit(t *testing.T) {
	rules := houseRockets.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 300*time.Millisecond, 600*time.Millisecond
	fixture := newRocketsGatewayFixture(t, rules)
	session := fixture.ensureHTTP(t)
	first := fixture.connect(t, 0, session.SessionId, fixture.ownerID)
	second := fixture.connect(t, 1, session.SessionId, fixture.memberID)
	first.lifecycle(t, gameCommands.JoinGameSessionCommandType, "join-first", struct{}{})
	second.lifecycle(t, gameCommands.JoinGameSessionCommandType, "join-second", struct{}{})
	first.lifecycle(t, gameCommands.SetPlayerReadyCommandType, "ready-first", dtos.RealtimeReadyModel{Ready: true})
	second.lifecycle(t, gameCommands.SetPlayerReadyCommandType, "ready-second", dtos.RealtimeReadyModel{Ready: true})
	countdown := first.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhaseCountdown
	})
	if countdown.CountdownEndsAt == nil || countdown.Tick != 0 || len(countdown.Players) != 2 || countdown.Players[0].DisplayName != "Player0 House" {
		t.Fatalf("countdown: %+v", countdown)
	}
	id, _ := primitive.ObjectIDFromHex(fixture.ownerID)
	if _, err := fixture.db.Collection("User").UpdateOne(fixture.ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"firstName": "Changed"}}); err != nil {
		t.Fatal(err)
	}
	grant1, grant2 := first.grant(t), second.grant(t)
	if grant1.RuntimeEpoch != grant2.RuntimeEpoch || grant1.ControlGeneration == grant2.ControlGeneration {
		t.Fatalf("grants: %+v %+v", grant1, grant2)
	}
	playing := first.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhasePlaying && model.Players[0].ControlGeneration != nil
	})
	if playing.StateSequence <= countdown.StateSequence || playing.Players[0].DisplayName != "Player0 House" || playing.Players[1].ControlGeneration != nil {
		t.Fatalf("frozen/private roster: %+v", playing)
	}
	for sequence := int64(1); sequence <= 36; sequence++ {
		first.send(t, houseRockets.SteerMessageType, fmt.Sprintf("first-%d", sequence), houseRockets.HouseRocketsSteerModel{ControlGeneration: grant1.ControlGeneration, InputSequence: sequence, Heading: 0.1})
		second.send(t, houseRockets.SteerMessageType, fmt.Sprintf("second-%d", sequence), houseRockets.HouseRocketsSteerModel{ControlGeneration: grant2.ControlGeneration, InputSequence: sequence, Heading: -0.1})
		time.Sleep(50 * time.Millisecond)
	}
	visible := second.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Players[0].LastProcessedInputSequence == 36 && model.Players[1].LastProcessedInputSequence == 36
	})
	if visible.Players[0].ControlGeneration != nil || visible.Players[1].ControlGeneration == nil || visible.Players[0].WorldX <= playing.Players[0].WorldX || math.Abs(visible.Players[0].CourseHeading-0.1) > 1e-12 {
		t.Fatalf("remote motion/ACK/privacy: %+v", visible)
	}
	second.send(t, houseRockets.ResyncMessageType, "resync", struct{}{})
	resynced := second.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == houseRockets.SnapshotMessageType && message.MessageID == "resync"
	})
	if len(resynced.Payload) == 0 {
		t.Fatal("empty resync")
	}
	first.lifecycle(t, gameCommands.LeaveGameSessionCommandType, "forfeit", struct{}{})
	final := second.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhaseFinalizing
	})
	if final.WinnerID != nil || final.Players[0].IsAlive || final.Players[0].EliminationReason == nil || *final.Players[0].EliminationReason != houseRockets.EliminationForfeit {
		t.Fatalf("authoritative forfeit: %+v", final)
	}
	stored, err := fixture.repository.FindByID(fixture.ctx, session.SessionId)
	if err != nil || stored.Snapshot().State != gameDomain.SessionRunning {
		t.Fatalf("P4 must not fake result completion: %v", err)
	}
}

func TestHouseRocketsGatewayMembershipRevocationClosesSocketAndEliminates(t *testing.T) {
	fixture := newRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := persistRocketsSession(t, fixture.gameSessionApplicationFixture, true)
	first := fixture.connect(t, 0, session.SessionID, fixture.ownerID)
	second := fixture.connect(t, 1, session.SessionID, fixture.memberID)
	_ = first.grant(t)
	_ = second.grant(t)
	houseID, _ := primitive.ObjectIDFromHex(fixture.houseID)
	if _, err := fixture.db.Collection("House").UpdateOne(fixture.ctx, bson.M{"_id": houseID}, bson.M{"$pull": bson.M{"memberIds": fixture.memberID}}); err != nil {
		t.Fatal(err)
	}
	final := first.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhaseFinalizing
	})
	if final.Players[1].EliminationReason == nil || *final.Players[1].EliminationReason != houseRockets.EliminationMembershipRevoked {
		t.Fatalf("revoked player: %+v", final.Players[1])
	}
	select {
	case <-second.errors:
	case <-time.After(2 * time.Second):
		t.Fatal("revoked socket stayed open")
	}
}

func TestHouseRocketsGatewayRequiresV2AndHouseMembership(t *testing.T) {
	fixture := newRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := fixture.ensureHTTP(t)
	endpoint := "ws" + fixture.urls[0][4:] + "/api/v1/game/" + session.SessionId + "/realtime"
	for _, check := range []struct {
		query, userID string
		status        int
	}{{"", fixture.ownerID, 400}, {"?protocolVersion=99", fixture.ownerID, 400}, {"?protocolVersion=2", fixture.outsiderID, 403}, {"?protocolVersion=2", "", 401}} {
		header := http.Header{}
		if check.userID != "" {
			header.Set("Authorization", "Bearer "+generateGatewayToken(t, fixture.jwt, check.userID))
		}
		connection, response, err := ws.DefaultDialer.Dial(endpoint+check.query, header)
		if connection != nil {
			_ = connection.Close()
		}
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		if err == nil || response == nil || response.StatusCode != check.status {
			t.Fatalf("upgrade %s: status=%v err=%v", check.query, responseStatus(response), err)
		}
	}
	player := fixture.connect(t, 0, session.SessionId, fixture.ownerID)
	player.send(t, realtime.PingMessageType, "ping", dtos.RealtimePingModel{PingID: "ping-1"})
	pong := player.await(t, func(message realtime.ServerMessage) bool { return message.Type == realtime.PongMessageType })
	var payload dtos.RealtimePongModel
	if json.Unmarshal(pong.Payload, &payload) != nil || payload.PingID != "ping-1" || payload.ServerTime.IsZero() {
		t.Fatalf("pong: %s", pong.Payload)
	}
}

func TestHouseRocketsGatewaySpectatorAndControllerTakeover(t *testing.T) {
	fixture := newRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := persistRocketsSession(t, fixture.gameSessionApplicationFixture, true)
	houseID, _ := primitive.ObjectIDFromHex(fixture.houseID)
	if _, err := fixture.db.Collection("House").UpdateOne(fixture.ctx, bson.M{"_id": houseID}, bson.M{"$addToSet": bson.M{"memberIds": fixture.outsiderID}}); err != nil {
		t.Fatal(err)
	}
	first := fixture.connect(t, 0, session.SessionID, fixture.ownerID)
	second := fixture.connect(t, 1, session.SessionID, fixture.memberID)
	oldGrant := first.grant(t)
	_ = second.grant(t)
	second.send(t, houseRockets.SteerMessageType, "remote-invalid", houseRockets.HouseRocketsSteerModel{ControlGeneration: "wrong-generation", InputSequence: 1, Heading: 0})
	rejection := second.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "remote-invalid"
	})
	if rejection.Error == nil || rejection.Error.Code != houseRockets.StaleControlErrorCode {
		t.Fatalf("remote rejection: %+v", rejection)
	}
	spectator := fixture.connect(t, 1, session.SessionID, fixture.outsiderID)
	view := spectator.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhasePlaying
	})
	if len(view.Players) != 2 || view.Players[0].ControlGeneration != nil || view.Players[1].ControlGeneration != nil {
		t.Fatalf("spectator: %+v", view)
	}
	spectator.send(t, houseRockets.SteerMessageType, "spectator-steer", houseRockets.HouseRocketsSteerModel{ControlGeneration: "invented", InputSequence: 1, Heading: 0})
	spectator.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "spectator-steer"
	})
	replacement := fixture.connect(t, 1, session.SessionID, fixture.ownerID)
	newGrant := replacement.grant(t)
	if newGrant.RuntimeEpoch != oldGrant.RuntimeEpoch || newGrant.ControlGeneration == oldGrant.ControlGeneration {
		t.Fatalf("takeover: %+v %+v", oldGrant, newGrant)
	}
	first.send(t, houseRockets.SteerMessageType, "stale-controller", houseRockets.HouseRocketsSteerModel{ControlGeneration: oldGrant.ControlGeneration, InputSequence: 1, Heading: 0})
	rejection = first.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "stale-controller"
	})
	if rejection.Error == nil || rejection.Error.Code != houseRockets.StaleControlErrorCode {
		t.Fatalf("old control: %+v", rejection)
	}
	replacement.send(t, houseRockets.SteerMessageType, "new-controller", houseRockets.HouseRocketsSteerModel{ControlGeneration: newGrant.ControlGeneration, InputSequence: 1, Heading: 0.2})
	updated := replacement.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Players[0].LastProcessedInputSequence == 1
	})
	if updated.Players[0].ControlGeneration == nil {
		t.Fatal("replacement has no generation")
	}
}

func TestHouseRocketsGatewayReadyRequiresApplicationHeartbeat(t *testing.T) {
	rules := houseRockets.Definition().Rules
	rules.ReadyWindowDuration = 8 * time.Second
	fixture := newRocketsGatewayFixture(t, rules)
	session := fixture.ensureHTTP(t)
	first := fixture.connect(t, 0, session.SessionId, fixture.ownerID)
	second := fixture.connect(t, 1, session.SessionId, fixture.memberID)
	first.lifecycle(t, gameCommands.JoinGameSessionCommandType, "join-first", struct{}{})
	second.lifecycle(t, gameCommands.JoinGameSessionCommandType, "join-second", struct{}{})
	first.lifecycle(t, gameCommands.SetPlayerReadyCommandType, "ready-first", dtos.RealtimeReadyModel{Ready: true})
	second.lifecycle(t, gameCommands.SetPlayerReadyCommandType, "ready-second", dtos.RealtimeReadyModel{Ready: true})
	for index := 0; index < 3; index++ {
		id := fmt.Sprintf("heartbeat-%d", index)
		first.send(t, realtime.PingMessageType, id, dtos.RealtimePingModel{PingID: id})
		first.await(t, func(message realtime.ServerMessage) bool {
			return message.Type == realtime.PongMessageType && message.MessageID == id
		})
		time.Sleep(2 * time.Second)
	}
	// The second socket continuously reads websocket ping frames and answers
	// transport pong, but never sends the application-level heartbeat.
	first.await(t, func(message realtime.ServerMessage) bool {
		if message.Type != realtime.GameSessionSnapshotEventType {
			return false
		}
		var model dtos.GameSessionResponseModel
		if json.Unmarshal(message.Payload, &model) != nil {
			return false
		}
		if model.State != string(gameDomain.SessionLobby) {
			return false
		}
		for _, player := range model.Players {
			if player.PlayerId == fixture.memberID && player.State == string(gameDomain.PlayerLeft) {
				return true
			}
		}
		return false
	})
	stored, err := fixture.repository.FindByID(fixture.ctx, session.SessionId)
	if err != nil || stored.Snapshot().State != gameDomain.SessionLobby {
		t.Fatalf("inactive ready player started game: %v", err)
	}
}

func TestHouseRocketsGatewayDisabledAndTokenExpiry(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		fixture := newRocketsGatewayFixture(t, houseRockets.Definition().Rules, false)
		session := fixture.ensureHTTP(t)
		header := http.Header{"Authorization": []string{"Bearer " + generateGatewayToken(t, fixture.jwt, fixture.ownerID)}}
		connection, response, err := ws.DefaultDialer.Dial("ws"+fixture.urls[0][4:]+"/api/v1/game/"+session.SessionId+"/realtime?protocolVersion=2", header)
		if connection != nil {
			_ = connection.Close()
		}
		if response != nil {
			_ = response.Body.Close()
		}
		if err == nil || response == nil || response.StatusCode != 404 {
			t.Fatalf("disabled upgrade: %v %v", responseStatus(response), err)
		}
	})
	t.Run("tokenExpiry", func(t *testing.T) {
		fixture := newRocketsGatewayFixture(t, houseRockets.Definition().Rules)
		session := fixture.ensureHTTP(t)
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": fixture.ownerID, "iss": "test@houseflow.dev", "exp": time.Now().Add(2 * time.Second).Unix(), "iat": time.Now().Unix(), "language": "eng", "role": 0}).SignedString([]byte("rockets-gateway-secret"))
		if err != nil {
			t.Fatal(err)
		}
		connection := dialGateway(t, "ws"+fixture.urls[0][4:]+"/api/v1/game/"+session.SessionId+"/realtime?protocolVersion=2", token)
		defer connection.Close()
		_ = connection.SetReadDeadline(time.Now().Add(4 * time.Second))
		started := time.Now()
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				break
			}
		}
		if time.Since(started) > 3*time.Second {
			t.Fatal("expired token did not close socket promptly")
		}
	})
}
