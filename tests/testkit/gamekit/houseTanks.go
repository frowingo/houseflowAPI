package gamekit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	"houseflowApi/internal/application/game/games/houseTanks"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/core"
	"houseflowApi/internal/models/dtos"
)

// emptyGameConfigs is a deployment without any Game document.
type EmptyGameConfigs struct{}

func (EmptyGameConfigs) List(context.Context) ([]gameAbstract.GameConfigRecord, error) {
	return nil, nil
}

func (EmptyGameConfigs) Save(_ context.Context, record gameAbstract.GameConfigRecord, _ int64) (gameAbstract.GameConfigRecord, error) {
	return record, nil
}

// keepAlive sends the application pings a client sends, so controls do not
// time out during a long test.
func (socket *RocketsSocket) KeepAlive(t *testing.T) {
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for index := 0; ; index++ {
			select {
			case <-done:
				return
			case <-ticker.C:
				raw, _ := json.Marshal(dtos.RealtimePingModel{PingID: fmt.Sprintf("keep-alive-%d", index)})
				socket.Mutex.Lock()
				err := socket.Connection.WriteJSON(realtime.ClientMessage{ProtocolVersion: 2, MessageID: fmt.Sprintf("keep-alive-%d", index), Type: realtime.PingMessageType, Payload: raw})
				socket.Mutex.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()
}

func (socket *RocketsSocket) TanksSnapshot(t *testing.T, predicate func(houseTanks.HouseTanksSnapshotModel) bool) houseTanks.HouseTanksSnapshotModel {
	t.Helper()
	var snapshot houseTanks.HouseTanksSnapshotModel
	socket.Await(t, func(message realtime.ServerMessage) bool {
		if message.Type != houseTanks.SnapshotMessageType {
			return false
		}
		snapshot = houseTanks.HouseTanksSnapshotModel{}
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		return predicate(snapshot)
	})
	return snapshot
}

// tanksGrant waits for a grant newer than the given epoch; frames may arrive
// before or after it.
func (socket *RocketsSocket) TanksGrant(t *testing.T, afterEpoch int64) houseTanks.HouseTanksControlGrantedModel {
	t.Helper()
	var grant houseTanks.HouseTanksControlGrantedModel
	socket.Await(t, func(message realtime.ServerMessage) bool {
		if message.Type != houseTanks.ControlGrantedMessageType {
			return false
		}
		if err := json.Unmarshal(message.Payload, &grant); err != nil {
			t.Fatal(err)
		}
		return grant.RuntimeEpoch > afterEpoch
	})
	return grant
}

func (socket *RocketsSocket) Rejection(t *testing.T, messageID string) realtime.ServerMessage {
	t.Helper()
	return socket.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == messageID
	})
}

// tanksResultHTTP reads the committed result as the API serves it, so it can
// be compared byte for byte with the socket's result message.
func (fixture *RocketsGatewayFixture) TanksResultHTTP(t *testing.T, index int, sessionID, userID string) (int, json.RawMessage) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, fixture.URLs[index]+"/api/v1/game/"+sessionID+"/result", nil)
	request.Header.Set("Authorization", "Bearer "+GenerateGatewayToken(t, fixture.JWT, userID))
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body core.ApiResponse[json.RawMessage]
	if response.StatusCode != 200 {
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode, nil
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || !body.Success {
		t.Fatalf("result response: %v", err)
	}
	return response.StatusCode, body.Data
}
