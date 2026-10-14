package realtime

import (
	"encoding/json"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
)

// turnStateMessage renders a turn game's state as this connection's player
// may see it. Sessions without a turn state have none.
func (client *gatewayClient) turnStateMessage(messageID string, snapshot gameDomain.SessionSnapshot, sentAt time.Time) ([]byte, bool) {
	turn := client.v2.turn
	if turn == nil || len(snapshot.GameState) == 0 || snapshot.SessionID != client.roomID {
		return nil, false
	}
	view, err := turn.View(module.Turn{Session: snapshot, Now: time.Now().UTC()}, client.userID)
	if err != nil {
		client.gateway.report(client.roomID, err)
		return nil, false
	}
	encoded, err := json.Marshal(NewV2ServerMessage(module.StateMessageType(snapshot.GameKey), messageID, &snapshot.Version, sentAt, view))
	return encoded, err == nil
}

// enqueueTurnState follows every session snapshot of a turn game with the
// state view it produced.
func (client *gatewayClient) enqueueTurnState(messageID string, snapshot gameDomain.SessionSnapshot, sentAt time.Time) {
	if encoded, exists := client.turnStateMessage(messageID, snapshot, sentAt); exists {
		client.enqueueV2(json.RawMessage(encoded), "", 0)
	}
}
