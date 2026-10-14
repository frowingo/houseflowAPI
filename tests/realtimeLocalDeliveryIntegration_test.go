package tests

import (
	"testing"
	"time"

	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
)

// Frames of a room whose sockets are all on the owner never cross Redis. A
// socket joining on another instance starts receiving them at once, and the
// owner's own sockets keep receiving them exactly once.
func TestRealtimeFramesUseRedisOnlyForRemoteSockets(t *testing.T) {
	fixture := newRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := persistRocketsSession(t, fixture.gameSessionApplicationFixture, true)
	owner := fixture.connect(t, 0, session.SessionID, fixture.ownerID)
	_ = owner.grant(t)
	if owned := fixture.services[0].Metrics().ActiveRooms; owned != 1 {
		t.Fatalf("first instance owns %d rooms", owned)
	}
	// Let the owner learn that no other instance holds a socket.
	time.Sleep(1500 * time.Millisecond)
	before := fixture.services[0].Metrics()
	time.Sleep(time.Second)
	after := fixture.services[0].Metrics()
	local := after.LocalOnlyEvents - before.LocalOnlyEvents
	published := after.RoomPublishedEvents - before.RoomPublishedEvents
	if local < 15 || published != 0 {
		t.Fatalf("one second with only local sockets: %d local frames, %d Redis publications", local, published)
	}

	seen := map[int64]bool{}
	first := owner.snapshot(t, func(houseRockets.HouseRocketsSnapshotModel) bool { return true })
	for range 10 {
		next := owner.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
			return model.StateSequence > first.StateSequence
		})
		if seen[next.StateSequence] {
			t.Fatalf("frame %d delivered twice", next.StateSequence)
		}
		seen[next.StateSequence] = true
		first = next
	}

	remote := fixture.connect(t, 1, session.SessionID, fixture.memberID)
	_ = remote.grant(t)
	start := remote.snapshot(t, func(houseRockets.HouseRocketsSnapshotModel) bool { return true })
	deadline := time.Now().Add(time.Second)
	frames := 0
	for frames < 10 {
		next := remote.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
			return model.StateSequence > start.StateSequence
		})
		start = next
		frames++
	}
	if time.Now().After(deadline) {
		t.Fatalf("remote socket needed more than a second for 10 frames")
	}
	if published := fixture.services[0].Metrics().RoomPublishedEvents - after.RoomPublishedEvents; published < 10 {
		t.Fatalf("frames for a remote socket were not published: %d", published)
	}
	for range 5 {
		next := owner.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
			return model.StateSequence > first.StateSequence
		})
		if seen[next.StateSequence] {
			t.Fatalf("frame %d delivered twice to the owner's socket", next.StateSequence)
		}
		seen[next.StateSequence] = true
		first = next
	}
}
