package realtime

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
)

const fanoutBenchmarkPlayers = 8

// playingFrame returns an authoritative frame after several seconds of play so
// gates and speed fields have their steady-state sizes.
func playingFrame(b *testing.B) (houseRockets.RuntimeFrame, []houseRockets.HouseRocketsControlGrantedModel) {
	b.Helper()
	now := time.Now()
	players := make([]houseRockets.PlayerIdentity, fanoutBenchmarkPlayers)
	for index := range players {
		players[index] = houseRockets.PlayerIdentity{PlayerID: fmt.Sprintf("player-%d", index), DisplayName: fmt.Sprintf("Player %d", index)}
	}
	runtime, err := houseRockets.NewRuntime(houseRockets.NewSimulationParams{SessionID: "fanout-session", HouseID: "fanout-house", StartedAt: now, Players: players}, 1, now)
	if err != nil {
		b.Fatal(err)
	}
	grants := make([]houseRockets.HouseRocketsControlGrantedModel, len(players))
	for index, player := range players {
		if err := runtime.Submit(houseRockets.RuntimeInput{Kind: houseRockets.InputBind, PlayerID: player.PlayerID, ConnectionID: connectionID(index), RuntimeEpoch: 1, CreatedAt: now}, now); err != nil {
			b.Fatal(err)
		}
		if err := runtime.Advance(now); err != nil {
			b.Fatal(err)
		}
		for _, event := range drainEvents(runtime) {
			if event.Grant != nil {
				grants[index] = *event.Grant
			}
		}
	}
	for step := 1; step <= 5*houseRockets.SchedulingRateHz; step++ {
		at := now.Add(time.Duration(step) * time.Second / houseRockets.SchedulingRateHz)
		for index, grant := range grants {
			input := houseRockets.RuntimeInput{Kind: houseRockets.InputSteer, PlayerID: grant.PlayerID, ConnectionID: connectionID(index), RuntimeEpoch: 1, ControlGeneration: grant.ControlGeneration, InputSequence: int64(step), CreatedAt: at}
			_ = runtime.Submit(input, at)
		}
		if err := runtime.Advance(at); err != nil {
			b.Fatal(err)
		}
		drainEvents(runtime)
	}
	select {
	case frame := <-runtime.Frames():
		if frame.Phase != houseRockets.PhasePlaying || len(frame.World.Gates) == 0 {
			b.Fatalf("frame phase=%s gates=%d", frame.Phase, len(frame.World.Gates))
		}
		return frame, grants
	default:
		b.Fatal("missing runtime frame")
		return houseRockets.RuntimeFrame{}, nil
	}
}

func drainEvents(runtime *houseRockets.Runtime) []houseRockets.RuntimeEvent {
	var events []houseRockets.RuntimeEvent
	for {
		select {
		case event := <-runtime.Events():
			events = append(events, event)
		default:
			return events
		}
	}
}

func connectionID(index int) string { return fmt.Sprintf("connection-%d", index) }

// BenchmarkHouseRocketsFramePublish measures owner-side work per 20 Hz frame:
// checkpoint critical key and the Redis room event payload.
func BenchmarkHouseRocketsFramePublish(b *testing.B) {
	frame, _ := playingFrame(b)
	payload, err := json.Marshal(frame)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = frame.Checkpoint.CriticalKey()
		if _, err := json.Marshal(frame); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(payload)), "roomEventBytes")
}

// BenchmarkHouseRocketsFrameFanout measures the gateway work for delivering
// one runtime frame to every controlling connection of a full room.
func BenchmarkHouseRocketsFrameFanout(b *testing.B) {
	frame, grants := playingFrame(b)
	payload, err := json.Marshal(gamePreviewFrame{RuntimeFrame: frame})
	if err != nil {
		b.Fatal(err)
	}
	gateway := &Gateway{options: GatewayOptions{Metrics: &Metrics{}}}
	hub := &roomHub{clients: make(map[string]*gatewayClient, len(grants))}
	clients := make([]*gatewayClient, len(grants))
	for index := range grants {
		client := &gatewayClient{id: connectionID(index), roomID: "fanout-session", userID: grants[index].PlayerID, gateway: gateway, outbound: make(chan []byte, defaultOutboundQueueSize), active: true, v2: newV2ClientState(1)}
		client.v2.grant = &grants[index]
		clients[index] = client
		hub.clients[client.id] = client
	}
	event := coordinationAbstract.MessageEnvelope{MessageID: "frame", Type: GameRuntimeSnapshotEventType, Sequence: frame.StateSequence, Payload: payload}
	var wireBytes int
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, client := range clients {
			client.v2.sequence = 0
		}
		hub.broadcastEvent(event)
		wireBytes = 0
		for _, client := range clients {
			select {
			case message := <-client.v2.snapshots:
				wireBytes += len(message)
			default:
				b.Fatal("frame was not delivered")
			}
			select {
			case <-client.v2.controlSignal:
			default:
			}
		}
	}
	b.ReportMetric(float64(len(payload)), "roomEventBytes")
	b.ReportMetric(float64(wireBytes)/float64(len(clients)), "wireBytes/connection")
}
