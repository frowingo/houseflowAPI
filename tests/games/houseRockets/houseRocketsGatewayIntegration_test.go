package houseRockets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit/gamekit"

	ws "github.com/fasthttp/websocket"
	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestHouseRocketsGatewayTwoInstancesHTTPCountdownSteeringAndForfeit(t *testing.T) {
	rules := houseRockets.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 300*time.Millisecond, 600*time.Millisecond
	fixture := gamekit.NewRocketsGatewayFixture(t, rules)
	session := fixture.EnsureHTTP(t)
	first := fixture.Connect(t, 0, session.SessionId, fixture.OwnerID)
	second := fixture.Connect(t, 1, session.SessionId, fixture.MemberID)
	first.Lifecycle(t, gameCommands.JoinGameSessionCommandType, "join-first", struct{}{})
	second.Lifecycle(t, gameCommands.JoinGameSessionCommandType, "join-second", struct{}{})
	first.Lifecycle(t, gameCommands.SetPlayerReadyCommandType, "ready-first", dtos.RealtimeReadyModel{Ready: true})
	second.Lifecycle(t, gameCommands.SetPlayerReadyCommandType, "ready-second", dtos.RealtimeReadyModel{Ready: true})
	countdown := first.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhaseCountdown
	})
	if countdown.CountdownEndsAt == nil || countdown.Tick != 0 || len(countdown.Players) != 2 || countdown.Players[0].DisplayName != "Player0 House" {
		t.Fatalf("countdown: %+v", countdown)
	}
	id, _ := primitive.ObjectIDFromHex(fixture.OwnerID)
	if _, err := fixture.DB.Collection("User").UpdateOne(fixture.Ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"firstName": "Changed"}}); err != nil {
		t.Fatal(err)
	}
	grant1, grant2 := first.Grant(t), second.Grant(t)
	if grant1.RuntimeEpoch != grant2.RuntimeEpoch || grant1.ControlGeneration == grant2.ControlGeneration {
		t.Fatalf("grants: %+v %+v", grant1, grant2)
	}
	playing := first.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhasePlaying && model.Players[0].ControlGeneration != nil
	})
	if playing.StateSequence <= countdown.StateSequence || playing.Players[0].DisplayName != "Player0 House" || playing.Players[1].ControlGeneration != nil {
		t.Fatalf("frozen/private roster: %+v", playing)
	}
	for sequence := int64(1); sequence <= 36; sequence++ {
		first.Send(t, houseRockets.SteerMessageType, fmt.Sprintf("first-%d", sequence), houseRockets.HouseRocketsSteerModel{ControlGeneration: grant1.ControlGeneration, InputSequence: sequence, Heading: 0.1})
		second.Send(t, houseRockets.SteerMessageType, fmt.Sprintf("second-%d", sequence), houseRockets.HouseRocketsSteerModel{ControlGeneration: grant2.ControlGeneration, InputSequence: sequence, Heading: -0.1})
		time.Sleep(50 * time.Millisecond)
	}
	visible := second.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Players[0].LastProcessedInputSequence == 36 && model.Players[1].LastProcessedInputSequence == 36
	})
	if visible.Players[0].ControlGeneration != nil || visible.Players[1].ControlGeneration == nil || visible.Players[0].WorldX <= playing.Players[0].WorldX || math.Abs(visible.Players[0].CourseHeading-0.1) > 1e-12 {
		t.Fatalf("remote motion/ACK/privacy: %+v", visible)
	}
	second.Send(t, houseRockets.ResyncMessageType, "resync", struct{}{})
	resynced := second.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == houseRockets.SnapshotMessageType && message.MessageID == "resync"
	})
	if len(resynced.Payload) == 0 {
		t.Fatal("empty resync")
	}
	first.Lifecycle(t, gameCommands.LeaveGameSessionCommandType, "forfeit", struct{}{})
	final := second.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhaseFinalizing
	})
	if final.WinnerID != nil || final.Players[0].IsAlive || final.Players[0].EliminationReason == nil || *final.Players[0].EliminationReason != houseRockets.EliminationForfeit {
		t.Fatalf("authoritative forfeit: %+v", final)
	}
	ended := second.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool { return model.Phase == houseRockets.PhaseEnded })
	if ended.WinnerID == nil || *ended.WinnerID != fixture.MemberID {
		t.Fatalf("ended winner: %+v", ended)
	}
	resultMessage := second.Await(t, func(message realtime.ServerMessage) bool { return message.Type == houseRockets.ResultMessageType })
	var result houseRockets.HouseRocketsResultModel
	if err := json.Unmarshal(resultMessage.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != houseRockets.ResultCompleted || result.WinnerID == nil || *result.WinnerID != fixture.MemberID {
		t.Fatalf("result: %+v", result)
	}
	stored, err := fixture.Repository.FindByID(fixture.Ctx, session.SessionId)
	if err != nil || stored.Snapshot().State != gameDomain.SessionFinished {
		t.Fatalf("completion not persisted: %v", err)
	}
	for index := range fixture.URLs {
		status, read := fixture.ResultHTTP(t, index, session.SessionId, fixture.OwnerID)
		if status != 200 || !bytes.Equal(resultMessage.Payload, gamekit.MustJSON(t, read)) {
			t.Fatalf("HTTP/socket canonical mismatch: %d %+v", status, read)
		}
	}
	status, _ := fixture.ResultHTTP(t, 0, session.SessionId, fixture.OutsiderID)
	if status != 403 {
		t.Fatalf("outsider result status: %d", status)
	}
	rematch := fixture.EnsureHTTP(t)
	if rematch.SessionId == session.SessionId || rematch.State != string(gameDomain.SessionLobby) || len(rematch.Players) != 0 {
		t.Fatalf("rematch reset old game: %+v", rematch)
	}
}

func TestHouseRocketsGatewayMembershipRevocationClosesSocketAndEliminates(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := gamekit.PersistRocketsSession(t, fixture.GameSessionApplicationFixture, true)
	first := fixture.Connect(t, 0, session.SessionID, fixture.OwnerID)
	second := fixture.Connect(t, 1, session.SessionID, fixture.MemberID)
	_ = first.Grant(t)
	_ = second.Grant(t)
	houseID, _ := primitive.ObjectIDFromHex(fixture.HouseID)
	if _, err := fixture.DB.Collection("House").UpdateOne(fixture.Ctx, bson.M{"_id": houseID}, bson.M{"$pull": bson.M{"memberIds": fixture.MemberID}}); err != nil {
		t.Fatal(err)
	}
	final := first.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhaseFinalizing
	})
	if final.Players[1].EliminationReason == nil || *final.Players[1].EliminationReason != houseRockets.EliminationMembershipRevoked {
		t.Fatalf("revoked player: %+v", final.Players[1])
	}
	select {
	case <-second.Errors:
	case <-time.After(2 * time.Second):
		t.Fatal("revoked socket stayed open")
	}
}

// Reconciliation re-reads membership every interval but announces it to the
// room's sockets only when the member set changes.
func TestHouseRocketsGatewayAnnouncesOnlyMembershipChanges(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := fixture.EnsureHTTP(t)
	_ = fixture.Connect(t, 0, session.SessionId, fixture.OwnerID)
	observer := gamekit.NewTestCoordinator(t, os.Getenv("HOUSEFLOW_TEST_REDIS_URL"), "access-observer")
	events, err := observer.SubscribeRoomEvents(fixture.Ctx, session.SessionId)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	countAccess := func(window time.Duration) int {
		count := 0
		timer := time.NewTimer(window)
		defer timer.Stop()
		for {
			select {
			case event := <-events.Messages():
				if event.Type == realtime.GameRoomAccessEventType {
					count++
				}
			case <-timer.C:
				return count
			}
		}
	}
	// Ten reconcile intervals without a membership change.
	if unchanged := countAccess(time.Second); unchanged != 0 {
		t.Fatalf("access events without membership change = %d, want 0", unchanged)
	}
	houseID, _ := primitive.ObjectIDFromHex(fixture.HouseID)
	if _, err := fixture.DB.Collection("House").UpdateOne(fixture.Ctx, bson.M{"_id": houseID}, bson.M{"$pull": bson.M{"memberIds": fixture.MemberID}}); err != nil {
		t.Fatal(err)
	}
	if changed := countAccess(time.Second); changed != 1 {
		t.Fatalf("access events after revocation = %d, want 1", changed)
	}
}

func TestHouseRocketsGatewayRequiresV2AndHouseMembership(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := fixture.EnsureHTTP(t)
	endpoint := "ws" + fixture.URLs[0][4:] + "/api/v1/game/" + session.SessionId + "/realtime"
	for _, check := range []struct {
		query, userID string
		status        int
	}{{"", fixture.OwnerID, 400}, {"?protocolVersion=99", fixture.OwnerID, 400}, {"?protocolVersion=2", fixture.OutsiderID, 403}, {"?protocolVersion=2", "", 401}} {
		header := http.Header{}
		if check.userID != "" {
			header.Set("Authorization", "Bearer "+gamekit.GenerateGatewayToken(t, fixture.JWT, check.userID))
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
			t.Fatalf("upgrade %s: status=%v err=%v", check.query, gamekit.ResponseStatus(response), err)
		}
	}
	player := fixture.Connect(t, 0, session.SessionId, fixture.OwnerID)
	player.Send(t, realtime.PingMessageType, "ping", dtos.RealtimePingModel{PingID: "ping-1"})
	pong := player.Await(t, func(message realtime.ServerMessage) bool { return message.Type == realtime.PongMessageType })
	var payload dtos.RealtimePongModel
	if json.Unmarshal(pong.Payload, &payload) != nil || payload.PingID != "ping-1" || payload.ServerTime.IsZero() {
		t.Fatalf("pong: %s", pong.Payload)
	}
}

func TestHouseRocketsGatewaySpectatorAndControllerTakeover(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := gamekit.PersistRocketsSession(t, fixture.GameSessionApplicationFixture, true)
	houseID, _ := primitive.ObjectIDFromHex(fixture.HouseID)
	if _, err := fixture.DB.Collection("House").UpdateOne(fixture.Ctx, bson.M{"_id": houseID}, bson.M{"$addToSet": bson.M{"memberIds": fixture.OutsiderID}}); err != nil {
		t.Fatal(err)
	}
	first := fixture.Connect(t, 0, session.SessionID, fixture.OwnerID)
	second := fixture.Connect(t, 1, session.SessionID, fixture.MemberID)
	oldGrant := first.Grant(t)
	_ = second.Grant(t)
	second.Send(t, houseRockets.SteerMessageType, "remote-invalid", houseRockets.HouseRocketsSteerModel{ControlGeneration: "wrong-generation", InputSequence: 1, Heading: 0})
	rejection := second.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "remote-invalid"
	})
	if rejection.Error == nil || rejection.Error.Code != houseRockets.StaleControlErrorCode {
		t.Fatalf("remote rejection: %+v", rejection)
	}
	spectator := fixture.Connect(t, 1, session.SessionID, fixture.OutsiderID)
	view := spectator.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhasePlaying
	})
	if len(view.Players) != 2 || view.Players[0].ControlGeneration != nil || view.Players[1].ControlGeneration != nil {
		t.Fatalf("spectator: %+v", view)
	}
	spectator.Send(t, houseRockets.SteerMessageType, "spectator-steer", houseRockets.HouseRocketsSteerModel{ControlGeneration: "invented", InputSequence: 1, Heading: 0})
	spectator.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "spectator-steer"
	})
	replacement := fixture.Connect(t, 1, session.SessionID, fixture.OwnerID)
	newGrant := replacement.Grant(t)
	if newGrant.RuntimeEpoch != oldGrant.RuntimeEpoch || newGrant.ControlGeneration == oldGrant.ControlGeneration {
		t.Fatalf("takeover: %+v %+v", oldGrant, newGrant)
	}
	first.Send(t, houseRockets.SteerMessageType, "stale-controller", houseRockets.HouseRocketsSteerModel{ControlGeneration: oldGrant.ControlGeneration, InputSequence: 1, Heading: 0})
	rejection = first.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "stale-controller"
	})
	if rejection.Error == nil || rejection.Error.Code != houseRockets.StaleControlErrorCode {
		t.Fatalf("old control: %+v", rejection)
	}
	replacement.Send(t, houseRockets.SteerMessageType, "new-controller", houseRockets.HouseRocketsSteerModel{ControlGeneration: newGrant.ControlGeneration, InputSequence: 1, Heading: 0.2})
	updated := replacement.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Players[0].LastProcessedInputSequence == 1
	})
	if updated.Players[0].ControlGeneration == nil {
		t.Fatal("replacement has no generation")
	}
}

func TestHouseRocketsGatewayReadyRequiresApplicationHeartbeat(t *testing.T) {
	rules := houseRockets.Definition().Rules
	rules.ReadyWindowDuration = 8 * time.Second
	fixture := gamekit.NewRocketsGatewayFixture(t, rules)
	session := fixture.EnsureHTTP(t)
	first := fixture.Connect(t, 0, session.SessionId, fixture.OwnerID)
	second := fixture.Connect(t, 1, session.SessionId, fixture.MemberID)
	first.Lifecycle(t, gameCommands.JoinGameSessionCommandType, "join-first", struct{}{})
	second.Lifecycle(t, gameCommands.JoinGameSessionCommandType, "join-second", struct{}{})
	first.Lifecycle(t, gameCommands.SetPlayerReadyCommandType, "ready-first", dtos.RealtimeReadyModel{Ready: true})
	second.Lifecycle(t, gameCommands.SetPlayerReadyCommandType, "ready-second", dtos.RealtimeReadyModel{Ready: true})
	for index := 0; index < 3; index++ {
		id := fmt.Sprintf("heartbeat-%d", index)
		first.Send(t, realtime.PingMessageType, id, dtos.RealtimePingModel{PingID: id})
		first.Await(t, func(message realtime.ServerMessage) bool {
			return message.Type == realtime.PongMessageType && message.MessageID == id
		})
		time.Sleep(2 * time.Second)
	}
	// The second socket continuously reads websocket ping frames and answers
	// transport pong, but never sends the application-level heartbeat.
	first.Await(t, func(message realtime.ServerMessage) bool {
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
			if player.PlayerId == fixture.MemberID && player.State == string(gameDomain.PlayerLeft) {
				return true
			}
		}
		return false
	})
	stored, err := fixture.Repository.FindByID(fixture.Ctx, session.SessionId)
	if err != nil || stored.Snapshot().State != gameDomain.SessionLobby {
		t.Fatalf("inactive ready player started game: %v", err)
	}
}

func TestHouseRocketsGatewayDisabledAndTokenExpiry(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules, false)
		session := fixture.EnsureHTTP(t)
		header := http.Header{"Authorization": []string{"Bearer " + gamekit.GenerateGatewayToken(t, fixture.JWT, fixture.OwnerID)}}
		connection, response, err := ws.DefaultDialer.Dial("ws"+fixture.URLs[0][4:]+"/api/v1/game/"+session.SessionId+"/realtime?protocolVersion=2", header)
		if connection != nil {
			_ = connection.Close()
		}
		if response != nil {
			_ = response.Body.Close()
		}
		if err == nil || response == nil || response.StatusCode != 404 {
			t.Fatalf("disabled upgrade: %v %v", gamekit.ResponseStatus(response), err)
		}
	})
	t.Run("tokenExpiry", func(t *testing.T) {
		fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
		session := fixture.EnsureHTTP(t)
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": fixture.OwnerID, "iss": "test@houseflow.dev", "exp": time.Now().Add(2 * time.Second).Unix(), "iat": time.Now().Unix(), "language": "eng", "role": 0}).SignedString([]byte("rockets-gateway-secret"))
		if err != nil {
			t.Fatal(err)
		}
		connection := gamekit.DialGateway(t, "ws"+fixture.URLs[0][4:]+"/api/v1/game/"+session.SessionId+"/realtime?protocolVersion=2", token)
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
