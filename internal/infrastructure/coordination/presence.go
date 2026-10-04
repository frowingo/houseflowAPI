package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"

	"github.com/redis/go-redis/v9"
)

var touchPresenceScript = redis.NewScript(
	"redis.call('HSET', KEYS[1], ARGV[1], ARGV[2]) " +
		"redis.call('ZADD', KEYS[2], ARGV[3], ARGV[1]) " +
		"redis.call('PEXPIRE', KEYS[1], ARGV[4]) " +
		"redis.call('PEXPIRE', KEYS[2], ARGV[4]) " +
		"return 1",
)

var removePresenceScript = redis.NewScript(
	"redis.call('HDEL', KEYS[1], ARGV[1]) " +
		"redis.call('ZREM', KEYS[2], ARGV[1]) " +
		"return 1",
)

var listPresenceScript = redis.NewScript(
	"local expired = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[1]) " +
		"for _, member in ipairs(expired) do " +
		"redis.call('HDEL', KEYS[1], member) " +
		"redis.call('ZREM', KEYS[2], member) end " +
		"local active = redis.call('ZRANGEBYSCORE', KEYS[2], '(' .. ARGV[1], '+inf') " +
		"local result = {} " +
		"for _, member in ipairs(active) do " +
		"local value = redis.call('HGET', KEYS[1], member) " +
		"if value then table.insert(result, value) end end " +
		"return result",
)

func (coordinator *RedisCoordinator) TouchPresence(
	ctx context.Context,
	presence coordinationAbstract.Presence,
	ttl time.Duration,
) error {
	if presence.RoomID == "" || presence.UserID == "" || presence.ConnectionID == "" {
		return errors.New("room ID, user ID and connection ID are required")
	}
	if ttl <= 0 {
		return errors.New("presence TTL must be greater than zero")
	}
	if presence.InstanceID != "" && presence.InstanceID != coordinator.instanceID {
		return errors.New("presence instance does not match the coordinator")
	}
	if err := coordinator.ensureOpen(); err != nil {
		return err
	}

	presence.InstanceID = coordinator.instanceID
	presence.ExpiresAt = time.Now().Add(ttl).UTC()
	payload, err := json.Marshal(presence)
	if err != nil {
		return err
	}
	memberID := presenceMemberID(coordinator.instanceID, presence.ConnectionID)
	retention := ttl * 2
	if retention < ttl {
		retention = ttl
	}
	if err := touchPresenceScript.Run(
		ctx,
		coordinator.client,
		[]string{presenceHashKey(presence.RoomID), presenceExpiryKey(presence.RoomID)},
		memberID,
		payload,
		presence.ExpiresAt.UnixMilli(),
		retention.Milliseconds(),
	).Err(); err != nil {
		coordinator.available.Store(false)
		return unavailableError(err)
	}

	coordinator.mutex.Lock()
	coordinator.presences[trackedPresenceKey(presence.RoomID, presence.ConnectionID)] = presence
	coordinator.mutex.Unlock()
	coordinator.available.Store(true)
	return nil
}

func (coordinator *RedisCoordinator) RemovePresence(
	ctx context.Context,
	roomID string,
	connectionID string,
) error {
	if err := coordinator.ensureOpen(); err != nil {
		return err
	}
	err := coordinator.removePresence(ctx, roomID, connectionID)
	if err == nil {
		coordinator.mutex.Lock()
		delete(coordinator.presences, trackedPresenceKey(roomID, connectionID))
		coordinator.mutex.Unlock()
	}
	return err
}

func (coordinator *RedisCoordinator) removePresence(
	ctx context.Context,
	roomID string,
	connectionID string,
) error {
	if roomID == "" || connectionID == "" {
		return errors.New("room ID and connection ID are required")
	}
	if err := removePresenceScript.Run(
		ctx,
		coordinator.client,
		[]string{presenceHashKey(roomID), presenceExpiryKey(roomID)},
		presenceMemberID(coordinator.instanceID, connectionID),
	).Err(); err != nil {
		coordinator.available.Store(false)
		return unavailableError(err)
	}
	coordinator.available.Store(true)
	return nil
}

func (coordinator *RedisCoordinator) ListPresence(
	ctx context.Context,
	roomID string,
) ([]coordinationAbstract.Presence, error) {
	if roomID == "" {
		return nil, errors.New("room ID is required")
	}
	if err := coordinator.ensureOpen(); err != nil {
		return nil, err
	}
	values, err := listPresenceScript.Run(
		ctx,
		coordinator.client,
		[]string{presenceHashKey(roomID), presenceExpiryKey(roomID)},
		time.Now().UnixMilli(),
	).StringSlice()
	if err != nil && !errors.Is(err, redis.Nil) {
		coordinator.available.Store(false)
		return nil, unavailableError(err)
	}

	presences := make([]coordinationAbstract.Presence, 0, len(values))
	for _, value := range values {
		var presence coordinationAbstract.Presence
		if err := json.Unmarshal([]byte(value), &presence); err != nil {
			return nil, err
		}
		presences = append(presences, presence)
	}
	coordinator.available.Store(true)
	return presences, nil
}

func trackedPresenceKey(roomID string, connectionID string) string {
	return roomID + "\x00" + connectionID
}
