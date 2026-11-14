package realtime

import (
	"encoding/json"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
)

// A resync the busy runtime refused is answered with the frame the connection
// already holds, correlated to the request, instead of a rejection that would
// make the client reconnect.
func TestBusyResyncIsAnsweredFromTheCachedFrame(t *testing.T) {
	frame, _ := playingFrame(t)
	payload, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	gateway := &Gateway{options: GatewayOptions{Metrics: &Metrics{}}}
	client := &gatewayClient{id: "spectator", roomID: "fanout-session", userID: "spectator", gateway: gateway, outbound: make(chan []byte, defaultOutboundQueueSize), active: true, v2: newV2ClientState(houseRockets.Module(), nil, 1)}
	next := func() (messageType, messageID string) {
		t.Helper()
		select {
		case raw := <-client.outbound:
			var message struct {
				Type      string `json:"type"`
				MessageID string `json:"messageId"`
			}
			if err := json.Unmarshal(raw, &message); err != nil {
				t.Fatal(err)
			}
			return message.Type, message.MessageID
		default:
			t.Fatal("nothing was sent")
			return "", ""
		}
	}
	busy, _ := json.Marshal(ProtocolError{Code: roomUnavailableErrorCode, Retryable: true})
	rejection := func(messageID string) coordinationAbstract.MessageEnvelope {
		return coordinationAbstract.MessageEnvelope{MessageID: messageID, Type: CommandRejectedMessageType, CreatedAt: time.Now(), Payload: busy}
	}

	if client.answerResyncFromCache("early") {
		t.Fatal("answered a resync before any frame arrived")
	}

	hub := &roomHub{clients: map[string]*gatewayClient{client.id: client}}
	hub.broadcastEvent(coordinationAbstract.MessageEnvelope{MessageID: "frame", Type: GameRuntimeSnapshotEventType, Sequence: frame.StateSequence, CreatedAt: time.Now(), Payload: payload})
	<-client.v2.snapshots

	// Local owner: the dispatch itself returned ErrRuntimeBusy.
	if !client.answerResyncFromCache("local-resync") {
		t.Fatal("cached frame was not used")
	}
	if messageType, messageID := next(); messageType != houseRockets.SnapshotMessageType || messageID != "local-resync" {
		t.Fatalf("local busy resync got %s for %q", messageType, messageID)
	}

	// Remote owner: the refusal comes back as a rejection event.
	client.v2.resyncID = "remote-resync"
	client.handleV2Event(rejection("remote-resync"), nil)
	if messageType, messageID := next(); messageType != houseRockets.SnapshotMessageType || messageID != "remote-resync" {
		t.Fatalf("remote busy resync got %s for %q", messageType, messageID)
	}

	// Any other rejected message is still rejected.
	client.handleV2Event(rejection("steer-1"), nil)
	if messageType, messageID := next(); messageType != CommandRejectedMessageType || messageID != "steer-1" {
		t.Fatalf("unrelated rejection got %s for %q", messageType, messageID)
	}
}
