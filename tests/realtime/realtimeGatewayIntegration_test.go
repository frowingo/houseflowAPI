package realtime

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit/gamekit"

	fasthttpWebsocket "github.com/fasthttp/websocket"
)

// Lobby commands, identity spoofing, protocol rejection, presence cleanup and
// reconnect recovery, with the two sockets connected to different instances.
func TestRealtimeGatewayAuthenticatesRoutesCommandsAndRecoversSnapshot(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := fixture.EnsureHTTP(t)
	observer := gamekit.NewTestCoordinator(t, os.Getenv("HOUSEFLOW_TEST_REDIS_URL"), "gateway-observer")
	member := fixture.Connect(t, 0, session.SessionId, fixture.MemberID)
	owner := fixture.Connect(t, 1, session.SessionId, fixture.OwnerID)
	gamekit.Eventually(t, 2*time.Second, func() bool {
		presences, err := observer.ListPresence(fixture.Ctx, session.SessionId)
		return err == nil && len(presences) == 2
	})

	owner.Send(t, gameCommands.SetPlayerReadyCommandType, "gateway-owner-not-player", realtime.ReadyCommandPayload{Ready: true})
	owner.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandAcceptedMessageType && message.MessageID == "gateway-owner-not-player"
	})
	rejected := owner.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "gateway-owner-not-player"
	})
	if rejected.Error == nil || rejected.Error.Code != "game.error.player_not_found" {
		t.Fatalf("application rejection = %+v", rejected)
	}

	member.Send(t, gameCommands.JoinGameSessionCommandType, "gateway-join-member", struct{}{})
	member.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandAcceptedMessageType && message.MessageID == "gateway-join-member"
	})
	for _, socket := range []*gamekit.RocketsSocket{member, owner} {
		joined := awaitSessionSnapshot(t, socket, "gateway-join-member")
		if joined.Version != 2 || len(joined.Players) != 1 || joined.Players[0].PlayerId != fixture.MemberID {
			t.Fatalf("joined snapshot = %+v", joined)
		}
	}

	member.Mutex.Lock()
	err := member.Connection.WriteMessage(fasthttpWebsocket.TextMessage, []byte(`{"protocolVersion":2,"messageId":"spoofed-actor","type":"`+gameCommands.LeaveGameSessionCommandType+`","actorId":"`+fixture.OwnerID+`"}`))
	member.Mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	spoofRejected := member.Await(t, func(message realtime.ServerMessage) bool { return message.Type == realtime.CommandRejectedMessageType })
	if spoofRejected.Error == nil || spoofRejected.Error.Code != "realtime.error.invalid_command" {
		t.Fatalf("spoof rejection = %+v", spoofRejected)
	}

	member.Mutex.Lock()
	err = member.Connection.WriteJSON(realtime.ClientMessage{ProtocolVersion: 99, MessageID: "unsupported-version", Type: gameCommands.LeaveGameSessionCommandType})
	member.Mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	versionRejected := member.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "unsupported-version"
	})
	if versionRejected.Error == nil || versionRejected.Error.Code != "realtime.error.unsupported_protocol" {
		t.Fatalf("protocol rejection = %+v", versionRejected)
	}

	_ = owner.Connection.Close()
	_ = member.Connection.Close()
	gamekit.Eventually(t, 2*time.Second, func() bool {
		presences, err := observer.ListPresence(fixture.Ctx, session.SessionId)
		return err == nil && len(presences) == 0
	})

	reconnected := fixture.Connect(t, 1, session.SessionId, fixture.MemberID)
	reconnected.Send(t, houseRockets.ResyncMessageType, "gateway-resync", struct{}{})
	recovered := awaitSessionSnapshot(t, reconnected, "gateway-resync")
	if recovered.Version != 2 || len(recovered.Players) != 1 || recovered.Players[0].PlayerId != fixture.MemberID {
		t.Fatalf("reconnected snapshot = %+v", recovered)
	}
}

func awaitSessionSnapshot(t *testing.T, socket *gamekit.RocketsSocket, messageID string) dtos.GameSessionResponseModel {
	t.Helper()
	message := socket.Await(t, func(message realtime.ServerMessage) bool {
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
