package coordination

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var acquireRoomScript = redis.NewScript(
	"if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end " +
		"local fencingToken = redis.call('INCR', KEYS[2]) " +
		"local leaseValue = ARGV[1] .. '|' .. ARGV[2] .. '|' .. tostring(fencingToken) " +
		"redis.call('PSETEX', KEYS[1], ARGV[3], leaseValue) " +
		"return fencingToken",
)

var renewRoomScript = redis.NewScript(
	"if redis.call('GET', KEYS[1]) == ARGV[1] then " +
		"return redis.call('PEXPIRE', KEYS[1], ARGV[2]) end " +
		"return 0",
)

var currentRoomOwnerScript = redis.NewScript(
	"local value = redis.call('GET', KEYS[1]) " +
		"if not value then return {} end " +
		"return {value, redis.call('PTTL', KEYS[1])}",
)

func (coordinator *RedisCoordinator) AcquireRoom(
	ctx context.Context,
	roomID string,
	ttl time.Duration,
) (coordinationAbstract.RoomLease, bool, error) {
	if err := validateLeaseInput(roomID, ttl); err != nil {
		return coordinationAbstract.RoomLease{}, false, err
	}
	if err := coordinator.ensureOpen(); err != nil {
		return coordinationAbstract.RoomLease{}, false, err
	}

	leaseID := uuid.NewString()
	fencingToken, err := acquireRoomScript.Run(
		ctx,
		coordinator.client,
		[]string{roomLeaseKey(roomID), roomFenceKey(roomID)},
		encodeKeyPart(coordinator.instanceID),
		leaseID,
		ttl.Milliseconds(),
	).Int64()
	if err != nil {
		coordinator.available.Store(false)
		return coordinationAbstract.RoomLease{}, false, unavailableError(err)
	}
	if fencingToken == 0 {
		return coordinationAbstract.RoomLease{}, false, nil
	}

	lease := coordinationAbstract.RoomLease{
		RoomID:          roomID,
		OwnerInstanceID: coordinator.instanceID,
		LeaseID:         leaseID,
		FencingToken:    fencingToken,
		ExpiresAt:       time.Now().Add(ttl),
	}
	coordinator.mutex.Lock()
	coordinator.leases[roomID] = lease
	coordinator.mutex.Unlock()
	coordinator.available.Store(true)
	return lease, true, nil
}

func (coordinator *RedisCoordinator) RenewRoom(
	ctx context.Context,
	lease coordinationAbstract.RoomLease,
	ttl time.Duration,
) (bool, error) {
	if err := coordinator.ensureOpen(); err != nil {
		return false, err
	}
	if err := validateLeaseInput(lease.RoomID, ttl); err != nil {
		return false, err
	}
	if lease.OwnerInstanceID != coordinator.instanceID || lease.LeaseID == "" || lease.FencingToken <= 0 {
		return false, coordinationAbstract.ErrLeaseLost
	}

	renewed, err := renewRoomScript.Run(
		ctx,
		coordinator.client,
		[]string{roomLeaseKey(lease.RoomID)},
		leaseValue(lease),
		ttl.Milliseconds(),
	).Int64()
	if err != nil {
		coordinator.available.Store(false)
		return false, unavailableError(err)
	}
	if renewed == 0 {
		coordinator.forgetLease(lease)
		return false, nil
	}

	lease.ExpiresAt = time.Now().Add(ttl)
	coordinator.mutex.Lock()
	coordinator.leases[lease.RoomID] = lease
	coordinator.mutex.Unlock()
	coordinator.available.Store(true)
	return true, nil
}

func (coordinator *RedisCoordinator) ReleaseRoom(
	ctx context.Context,
	lease coordinationAbstract.RoomLease,
) (bool, error) {
	if err := coordinator.ensureOpen(); err != nil {
		return false, err
	}
	released, err := coordinator.releaseRoom(ctx, lease)
	if err == nil || errors.Is(err, coordinationAbstract.ErrLeaseLost) {
		coordinator.forgetLease(lease)
	}
	return released, err
}

func (coordinator *RedisCoordinator) releaseRoom(
	ctx context.Context,
	lease coordinationAbstract.RoomLease,
) (bool, error) {
	if lease.RoomID == "" || lease.LeaseID == "" || lease.FencingToken <= 0 {
		return false, errors.New("valid room lease is required")
	}
	if lease.OwnerInstanceID != coordinator.instanceID {
		return false, coordinationAbstract.ErrLeaseLost
	}

	released, err := compareAndDeleteScript.Run(
		ctx,
		coordinator.client,
		[]string{roomLeaseKey(lease.RoomID)},
		leaseValue(lease),
	).Int64()
	if err != nil {
		coordinator.available.Store(false)
		return false, unavailableError(err)
	}
	coordinator.available.Store(true)
	return released == 1, nil
}

func (coordinator *RedisCoordinator) CurrentRoomOwner(
	ctx context.Context,
	roomID string,
) (coordinationAbstract.RoomLease, bool, error) {
	if roomID == "" {
		return coordinationAbstract.RoomLease{}, false, errors.New("room ID is required")
	}
	if err := coordinator.ensureOpen(); err != nil {
		return coordinationAbstract.RoomLease{}, false, err
	}

	result, err := currentRoomOwnerScript.Run(
		ctx,
		coordinator.client,
		[]string{roomLeaseKey(roomID)},
	).Slice()
	if errors.Is(err, redis.Nil) || len(result) == 0 {
		return coordinationAbstract.RoomLease{}, false, nil
	}
	if err != nil {
		coordinator.available.Store(false)
		return coordinationAbstract.RoomLease{}, false, unavailableError(err)
	}
	if len(result) != 2 {
		return coordinationAbstract.RoomLease{}, false, errors.New("invalid room lease response")
	}
	value, ok := result[0].(string)
	if !ok {
		return coordinationAbstract.RoomLease{}, false, errors.New("invalid room lease value")
	}
	ttlMilliseconds, ok := result[1].(int64)
	if !ok || ttlMilliseconds <= 0 {
		return coordinationAbstract.RoomLease{}, false, nil
	}

	lease, err := parseLeaseValue(roomID, value, time.Duration(ttlMilliseconds)*time.Millisecond)
	if err != nil {
		return coordinationAbstract.RoomLease{}, false, err
	}
	coordinator.available.Store(true)
	return lease, true, nil
}

func validateLeaseInput(roomID string, ttl time.Duration) error {
	if roomID == "" {
		return errors.New("room ID is required")
	}
	if ttl <= 0 {
		return errors.New("room lease TTL must be greater than zero")
	}
	return nil
}

func leaseValue(lease coordinationAbstract.RoomLease) string {
	return encodeKeyPart(lease.OwnerInstanceID) + "|" + lease.LeaseID + "|" + strconv.FormatInt(lease.FencingToken, 10)
}

func parseLeaseValue(roomID string, value string, ttl time.Duration) (coordinationAbstract.RoomLease, error) {
	parts := strings.Split(value, "|")
	if len(parts) != 3 {
		return coordinationAbstract.RoomLease{}, errors.New("invalid room lease value")
	}
	ownerInstanceID, err := decodeKeyPart(parts[0])
	if err != nil {
		return coordinationAbstract.RoomLease{}, fmt.Errorf("decode room owner: %w", err)
	}
	fencingToken, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return coordinationAbstract.RoomLease{}, fmt.Errorf("decode fencing token: %w", err)
	}
	return coordinationAbstract.RoomLease{
		RoomID:          roomID,
		OwnerInstanceID: ownerInstanceID,
		LeaseID:         parts[1],
		FencingToken:    fencingToken,
		ExpiresAt:       time.Now().Add(ttl),
	}, nil
}

func (coordinator *RedisCoordinator) forgetLease(lease coordinationAbstract.RoomLease) {
	coordinator.mutex.Lock()
	tracked, exists := coordinator.leases[lease.RoomID]
	if exists && tracked.LeaseID == lease.LeaseID {
		delete(coordinator.leases, lease.RoomID)
	}
	coordinator.mutex.Unlock()
}
