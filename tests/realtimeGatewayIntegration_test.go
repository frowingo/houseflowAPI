package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/middleware"
	"houseflowApi/internal/infrastructure/realtime"

	fasthttpWebsocket "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
)

func TestRealtimeGatewayAuthenticatesRoutesCommandsAndRecoversSnapshot(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	session := fixture.createSession(t)
	redisURL := prepareRedis(t)
	coordinator := newTestCoordinator(t, redisURL, "gateway-instance")
	jwtService := helpers.NewJWTService("gateway-test-secret")
	service, err := realtime.NewService(
		coordinator,
		fixture.repository,
		newGameSessionMediator(fixture),
		realtime.RoomManagerOptions{
			LeaseTTL:       5 * time.Second,
			RenewInterval:  500 * time.Millisecond,
			CommandTimeout: time.Second,
		},
		realtime.GatewayOptions{
			AllowedOrigins:    []string{"*"},
			PingInterval:      100 * time.Millisecond,
			IdleTimeout:       800 * time.Millisecond,
			WriteTimeout:      time.Second,
			PresenceTTL:       600 * time.Millisecond,
			PresenceRefresh:   100 * time.Millisecond,
			MessageRateWindow: time.Second,
			MaximumMessages:   20,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get(
		"/api/v1/game/:sessionId/realtime",
		middleware.AuthRequired(jwtService),
		service.UpgradeMiddleware(),
		service.Handler(),
	)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- app.Listener(listener) }()
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		_ = app.ShutdownWithContext(shutdownCtx)
		_ = service.Close(shutdownCtx)
		select {
		case <-serveErrors:
		case <-time.After(time.Second):
		}
	})
	endpoint := fmt.Sprintf(
		"ws://%s/api/v1/game/%s/realtime",
		listener.Addr().String(),
		session.SessionID,
	)

	unauthorized, response, err := fasthttpWebsocket.DefaultDialer.Dial(endpoint, nil)
	if unauthorized != nil {
		_ = unauthorized.Close()
	}
	if err == nil || response == nil || response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("unauthorized dial err=%v status=%v", err, responseStatus(response))
	}

	memberToken := generateGatewayToken(t, jwtService, fixture.memberID)
	ownerToken := generateGatewayToken(t, jwtService, fixture.ownerID)
	memberConnection := dialGateway(t, endpoint, memberToken)
	memberInitial := awaitGatewayMessage(t, memberConnection, realtime.GameSessionSnapshotEventType, "")
	assertGatewaySnapshot(t, memberInitial, gameDomain.SessionLobby, 1)
	ownerConnection := dialGateway(t, endpoint, ownerToken)
	ownerInitial := awaitGatewayMessage(t, ownerConnection, realtime.GameSessionSnapshotEventType, "")
	assertGatewaySnapshot(t, ownerInitial, gameDomain.SessionLobby, 1)

	eventually(t, time.Second, func() bool {
		presences, listErr := coordinator.ListPresence(fixture.ctx, session.SessionID)
		return listErr == nil && len(presences) == 2
	})
	readyPayload, err := json.Marshal(realtime.ReadyCommandPayload{Ready: true})
	if err != nil {
		t.Fatal(err)
	}
	writeGatewayMessage(t, ownerConnection, realtime.ClientMessage{
		ProtocolVersion: realtime.ProtocolVersion,
		MessageID:       "gateway-owner-not-player",
		Type:            gameCommands.SetPlayerReadyCommandType,
		Payload:         readyPayload,
	})
	awaitGatewayMessage(t, ownerConnection, realtime.CommandAcceptedMessageType, "gateway-owner-not-player")
	ownerRejected := awaitGatewayMessage(
		t,
		ownerConnection,
		realtime.CommandRejectedMessageType,
		"gateway-owner-not-player",
	)
	if ownerRejected.Error == nil || ownerRejected.Error.Code != "game.error.player_not_found" {
		t.Fatalf("application rejection = %+v", ownerRejected)
	}

	writeGatewayMessage(t, memberConnection, realtime.ClientMessage{
		ProtocolVersion: realtime.ProtocolVersion,
		MessageID:       "gateway-join-member",
		Type:            gameCommands.JoinGameSessionCommandType,
	})
	awaitGatewayMessage(t, memberConnection, realtime.CommandAcceptedMessageType, "gateway-join-member")
	memberSnapshot := awaitGatewayMessage(
		t,
		memberConnection,
		realtime.GameSessionSnapshotEventType,
		"gateway-join-member",
	)
	ownerSnapshot := awaitGatewayMessage(
		t,
		ownerConnection,
		realtime.GameSessionSnapshotEventType,
		"gateway-join-member",
	)
	assertGatewaySnapshot(t, memberSnapshot, gameDomain.SessionLobby, 2)
	assertGatewaySnapshot(t, ownerSnapshot, gameDomain.SessionLobby, 2)

	spoofedMessage := []byte(fmt.Sprintf(
		`{"protocolVersion":%d,"messageId":"spoofed-actor","type":"%s","actorId":"%s"}`,
		realtime.ProtocolVersion,
		gameCommands.LeaveGameSessionCommandType,
		fixture.ownerID,
	))
	if err := memberConnection.WriteMessage(fasthttpWebsocket.TextMessage, spoofedMessage); err != nil {
		t.Fatal(err)
	}
	spoofRejected := awaitGatewayMessage(
		t,
		memberConnection,
		realtime.CommandRejectedMessageType,
		"",
	)
	if spoofRejected.Error == nil || spoofRejected.Error.Code != "realtime.error.invalid_command" {
		t.Fatalf("spoof rejection = %+v", spoofRejected)
	}

	writeGatewayMessage(t, memberConnection, realtime.ClientMessage{
		ProtocolVersion: 99,
		MessageID:       "unsupported-version",
		Type:            gameCommands.LeaveGameSessionCommandType,
	})
	versionRejected := awaitGatewayMessage(
		t,
		memberConnection,
		realtime.CommandRejectedMessageType,
		"unsupported-version",
	)
	if versionRejected.Error == nil || versionRejected.Error.Code != "realtime.error.unsupported_protocol" {
		t.Fatalf("protocol rejection = %+v", versionRejected)
	}

	_ = ownerConnection.Close()
	_ = memberConnection.Close()
	eventually(t, 2*time.Second, func() bool {
		presences, listErr := coordinator.ListPresence(fixture.ctx, session.SessionID)
		return listErr == nil && len(presences) == 0
	})

	reconnected := dialGateway(t, endpoint, memberToken)
	defer reconnected.Close()
	reconnectedSnapshot := awaitGatewayMessage(
		t,
		reconnected,
		realtime.GameSessionSnapshotEventType,
		"",
	)
	assertGatewaySnapshot(t, reconnectedSnapshot, gameDomain.SessionLobby, 2)
	var snapshot gameDomain.SessionSnapshot
	if err := json.Unmarshal(reconnectedSnapshot.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Players) != 1 || snapshot.Players[0].PlayerID != fixture.memberID {
		t.Fatalf("reconnected players = %+v", snapshot.Players)
	}
}

func generateGatewayToken(t *testing.T, service *helpers.JWTService, userID string) string {
	t.Helper()
	token, err := service.GenerateToken(userID+"@houseflow.test", userID, 0, "eng")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func dialGateway(t *testing.T, endpoint string, token string) *fasthttpWebsocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	connection, response, err := fasthttpWebsocket.DefaultDialer.Dial(endpoint, header)
	if err != nil {
		t.Fatalf("dial realtime gateway: %v status=%v", err, responseStatus(response))
	}
	return connection
}

func responseStatus(response *http.Response) any {
	if response == nil {
		return nil
	}
	return response.StatusCode
}

func writeGatewayMessage(
	t *testing.T,
	connection *fasthttpWebsocket.Conn,
	message realtime.ClientMessage,
) {
	t.Helper()
	if err := connection.WriteJSON(message); err != nil {
		t.Fatal(err)
	}
}

func awaitGatewayMessage(
	t *testing.T,
	connection *fasthttpWebsocket.Conn,
	messageType string,
	messageID string,
) realtime.ServerMessage {
	t.Helper()
	deadline := time.Now().Add(roomRuntimeTestTimeout)
	if err := connection.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		var message realtime.ServerMessage
		if err := connection.ReadJSON(&message); err != nil {
			t.Fatalf("read gateway message: %v", err)
		}
		if message.Type == messageType && (messageID == "" || message.MessageID == messageID) {
			return message
		}
	}
	t.Fatalf("message type=%q id=%q was not delivered", messageType, messageID)
	return realtime.ServerMessage{}
}

func assertGatewaySnapshot(
	t *testing.T,
	message realtime.ServerMessage,
	state gameDomain.SessionState,
	version int64,
) {
	t.Helper()
	if message.ProtocolVersion != realtime.ProtocolVersion || message.Sequence != version {
		t.Fatalf("snapshot envelope = %+v", message)
	}
	var snapshot gameDomain.SessionSnapshot
	if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != state || snapshot.Version != version {
		t.Fatalf("snapshot state/version = %s/%d, want %s/%d", snapshot.State, snapshot.Version, state, version)
	}
}
