package tests

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/dtos"

	fasthttpWebsocket "github.com/fasthttp/websocket"
)

// Lobby commands, identity spoofing, protocol rejection, presence cleanup and
// reconnect recovery, with the two sockets connected to different instances.
func TestRealtimeGatewayAuthenticatesRoutesCommandsAndRecoversSnapshot(t *testing.T) {
	fixture := newRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := fixture.ensureHTTP(t)
	observer := newTestCoordinator(t, os.Getenv("HOUSEFLOW_TEST_REDIS_URL"), "gateway-observer")
	member := fixture.connect(t, 0, session.SessionId, fixture.memberID)
	owner := fixture.connect(t, 1, session.SessionId, fixture.ownerID)
	eventually(t, 2*time.Second, func() bool {
		presences, err := observer.ListPresence(fixture.ctx, session.SessionId)
		return err == nil && len(presences) == 2
	})

	owner.send(t, gameCommands.SetPlayerReadyCommandType, "gateway-owner-not-player", realtime.ReadyCommandPayload{Ready: true})
	owner.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandAcceptedMessageType && message.MessageID == "gateway-owner-not-player"
	})
	rejected := owner.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "gateway-owner-not-player"
	})
	if rejected.Error == nil || rejected.Error.Code != "game.error.player_not_found" {
		t.Fatalf("application rejection = %+v", rejected)
	}

	member.send(t, gameCommands.JoinGameSessionCommandType, "gateway-join-member", struct{}{})
	member.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandAcceptedMessageType && message.MessageID == "gateway-join-member"
	})
	for _, socket := range []*rocketsSocket{member, owner} {
		joined := awaitSessionSnapshot(t, socket, "gateway-join-member")
		if joined.Version != 2 || len(joined.Players) != 1 || joined.Players[0].PlayerId != fixture.memberID {
			t.Fatalf("joined snapshot = %+v", joined)
		}
	}

	member.mutex.Lock()
	err := member.connection.WriteMessage(fasthttpWebsocket.TextMessage, []byte(`{"protocolVersion":2,"messageId":"spoofed-actor","type":"`+gameCommands.LeaveGameSessionCommandType+`","actorId":"`+fixture.ownerID+`"}`))
	member.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	spoofRejected := member.await(t, func(message realtime.ServerMessage) bool { return message.Type == realtime.CommandRejectedMessageType })
	if spoofRejected.Error == nil || spoofRejected.Error.Code != "realtime.error.invalid_command" {
		t.Fatalf("spoof rejection = %+v", spoofRejected)
	}

	member.mutex.Lock()
	err = member.connection.WriteJSON(realtime.ClientMessage{ProtocolVersion: 99, MessageID: "unsupported-version", Type: gameCommands.LeaveGameSessionCommandType})
	member.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	versionRejected := member.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "unsupported-version"
	})
	if versionRejected.Error == nil || versionRejected.Error.Code != "realtime.error.unsupported_protocol" {
		t.Fatalf("protocol rejection = %+v", versionRejected)
	}

	_ = owner.connection.Close()
	_ = member.connection.Close()
	eventually(t, 2*time.Second, func() bool {
		presences, err := observer.ListPresence(fixture.ctx, session.SessionId)
		return err == nil && len(presences) == 0
	})

	reconnected := fixture.connect(t, 1, session.SessionId, fixture.memberID)
	reconnected.send(t, houseRockets.ResyncMessageType, "gateway-resync", struct{}{})
	recovered := awaitSessionSnapshot(t, reconnected, "gateway-resync")
	if recovered.Version != 2 || len(recovered.Players) != 1 || recovered.Players[0].PlayerId != fixture.memberID {
		t.Fatalf("reconnected snapshot = %+v", recovered)
	}
}

func awaitSessionSnapshot(t *testing.T, socket *rocketsSocket, messageID string) dtos.GameSessionResponseModel {
	t.Helper()
	message := socket.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.GameSessionSnapshotEventType && message.MessageID == messageID
	})
	var snapshot dtos.GameSessionResponseModel
	if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if message.Sequence != snapshot.Version {
		t.Fatalf("snapshot sequence = %d, version = %d", message.Sequence, snapshot.Version)
	}
	return snapshot
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
