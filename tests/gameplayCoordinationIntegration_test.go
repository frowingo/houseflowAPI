package tests

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
)

func TestGameplayCoordinationRequiresReadySubscriberAndNeverReplays(t *testing.T) {
	redisURL := prepareRedis(t)
	sender := newTestCoordinator(t, redisURL, "gameplay-sender")
	receiver := newTestCoordinator(t, redisURL, "gameplay-receiver")
	envelope := testEnvelope("before-subscription", "ephemeral-room")
	envelope.SourceInstanceID = ""
	if err := sender.PublishGameplay(t.Context(), receiver.InstanceID(), envelope); !errors.Is(err, coordinationAbstract.ErrUnavailable) {
		t.Fatalf("no subscriber: %v", err)
	}
	subscription, err := receiver.SubscribeGameplay(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	envelope.MessageID = "after-subscription"
	if err := sender.PublishGameplay(t.Context(), receiver.InstanceID(), envelope); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-subscription.Messages():
		if received.MessageID != envelope.MessageID {
			t.Fatal("old input was replayed")
		}
	case <-time.After(time.Second):
		t.Fatal("ready subscriber missed input")
	}
	envelope.Payload = json.RawMessage(`"` + strings.Repeat("x", 4096) + `"`)
	if err := sender.PublishGameplay(t.Context(), receiver.InstanceID(), envelope); err == nil {
		t.Fatal("oversize gameplay envelope accepted")
	}
	if err := subscription.Close(); err != nil {
		t.Fatal(err)
	}
	envelope.Payload = nil
	if err := sender.PublishGameplay(t.Context(), receiver.InstanceID(), envelope); !errors.Is(err, coordinationAbstract.ErrUnavailable) {
		t.Fatalf("closed subscriber: %v", err)
	}
}
