package realtime

import (
	"context"
	"sync"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
)

const (
	// audienceRefresh bounds how long a room keeps an outdated view of which
	// instances hold its sockets.
	audienceRefresh = time.Second
	// remoteInputGrace keeps frames on Redis after a remote socket spoke,
	// covering a presence read that started before that socket registered.
	remoteInputGrace = 3 * time.Second
)

// localAudience is this instance's gateway. Its sockets receive the rooms this
// instance owns from memory instead of a Redis round trip.
type localAudience interface {
	deliverLocal(event coordinationAbstract.MessageEnvelope)
	hasConnection(roomID, connectionID string) bool
}

// audienceState tracks whether another instance holds a socket of the room.
type audienceState struct {
	mutex         sync.Mutex
	remote        bool
	checkedAt     time.Time
	refreshing    bool
	remoteInputAt time.Time
}

// publish delivers a room event to every socket in publication order: through
// Redis (fenced by the lease) when another instance may need it, then to this
// instance's sockets in memory. Only a periodic frame with no remote audience,
// or an event targeted at a local connection, skips Redis.
func (room *managedRoom) publish(ctx context.Context, event coordinationAbstract.MessageEnvelope) error {
	room.publishMutex.Lock()
	defer room.publishMutex.Unlock()
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	local := room.manager.audience()
	event.RoomID, event.FencingToken = room.lease.RoomID, room.lease.FencingToken
	event.SourceInstanceID = room.manager.coordinator.InstanceID()
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if local == nil || room.needsRedis(local, event) {
		if err := room.manager.coordinator.PublishRoomEvent(ctx, room.lease, event); err != nil {
			return err
		}
		room.manager.options.Metrics.roomPublishedBytes.Add(uint64(len(event.Payload)))
		room.manager.options.Metrics.roomPublishedEvents.Add(1)
	} else {
		room.manager.options.Metrics.localOnlyEvents.Add(1)
	}
	if local != nil {
		local.deliverLocal(event)
	}
	return nil
}

func (room *managedRoom) needsRedis(local localAudience, event coordinationAbstract.MessageEnvelope) bool {
	if event.ConnectionID != "" {
		return !local.hasConnection(event.RoomID, event.ConnectionID)
	}
	return event.Type != GameRuntimeSnapshotEventType || room.remoteAudience()
}

// remoteAudience answers from a view at most audienceRefresh old and renews it
// in the background; an unknown or unreadable view counts as remote.
func (room *managedRoom) remoteAudience() bool {
	state := &room.audience
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if time.Since(state.checkedAt) >= audienceRefresh && !state.refreshing && room.manager.track() {
		state.refreshing = true
		go room.refreshAudience()
	}
	return state.checkedAt.IsZero() || state.remote || time.Since(state.remoteInputAt) < remoteInputGrace
}

func (room *managedRoom) refreshAudience() {
	defer room.manager.workers.Done()
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	presences, err := room.manager.coordinator.ListPresence(ctx, room.lease.RoomID)
	cancel()
	remote := err != nil
	for _, presence := range presences {
		remote = remote || presence.InstanceID != room.manager.coordinator.InstanceID()
	}
	state := &room.audience
	state.mutex.Lock()
	state.remote, state.checkedAt, state.refreshing = remote, time.Now(), false
	state.mutex.Unlock()
}

// noteRemoteInput records that a socket on another instance is in the room.
func (room *managedRoom) noteRemoteInput() {
	room.audience.mutex.Lock()
	room.audience.remoteInputAt = time.Now()
	room.audience.mutex.Unlock()
}
