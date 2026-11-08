package realtime

import (
	"testing"
	"time"

	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/tests/testkit/gamekit"
)

// Frames of a room whose sockets are all on the owner never cross Redis. A
// socket joining on another instance starts receiving them at once, and the
// owner's own sockets keep receiving them exactly once.
func TestRealtimeFramesUseRedisOnlyForRemoteSockets(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := gamekit.PersistRocketsSession(t, fixture.GameSessionApplicationFixture, true)
	owner := fixture.Connect(t, 0, session.SessionID, fixture.OwnerID)
	_ = owner.Grant(t)
	if owned := fixture.Services[0].Metrics().ActiveRooms; owned != 1 {
		t.Fatalf("first instance owns %d rooms", owned)
	}
	// Let the owner learn that no other instance holds a socket.
	time.Sleep(1500 * time.Millisecond)
	before := fixture.Services[0].Metrics()
	time.Sleep(time.Second)
	after := fixture.Services[0].Metrics()
	local := after.LocalOnlyEvents - before.LocalOnlyEvents
	published := after.RoomPublishedEvents - before.RoomPublishedEvents
	if local < 15 || published != 0 {
		t.Fatalf("one second with only local sockets: %d local frames, %d Redis publications", local, published)
	}

	seen := map[int64]bool{}
	first := owner.Snapshot(t, func(houseRockets.HouseRocketsSnapshotModel) bool { return true })
	for range 10 {
		next := owner.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
			return model.StateSequence > first.StateSequence
		})
		if seen[next.StateSequence] {
			t.Fatalf("frame %d delivered twice", next.StateSequence)
		}
		seen[next.StateSequence] = true
		first = next
	}

	remote := fixture.Connect(t, 1, session.SessionID, fixture.MemberID)
	_ = remote.Grant(t)
	start := remote.Snapshot(t, func(houseRockets.HouseRocketsSnapshotModel) bool { return true })
	deadline := time.Now().Add(time.Second)
	frames := 0
	for frames < 10 {
		next := remote.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
			return model.StateSequence > start.StateSequence
		})
		start = next
		frames++
	}
	if time.Now().After(deadline) {
		t.Fatalf("remote socket needed more than a second for 10 frames")
	}
	if published := fixture.Services[0].Metrics().RoomPublishedEvents - after.RoomPublishedEvents; published < 10 {
		t.Fatalf("frames for a remote socket were not published: %d", published)
	}
	for range 5 {
		next := owner.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
			return model.StateSequence > first.StateSequence
		})
		if seen[next.StateSequence] {
			t.Fatalf("frame %d delivered twice to the owner's socket", next.StateSequence)
		}
		seen[next.StateSequence] = true
		first = next
	}
}
