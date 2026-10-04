package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
)

func runtimeCheckpointKey(roomID string) string {
	return redisNamespace + ":room:" + encodeKeyPart(roomID) + ":checkpoint"
}

var saveRuntimeCheckpointScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local function greater(a, b)
  return string.len(a) > string.len(b) or (string.len(a) == string.len(b) and a > b)
end
local epoch = redis.call('HGET', KEYS[2], 'epoch')
local sequence = redis.call('HGET', KEYS[2], 'sequence')
if epoch and (greater(epoch, ARGV[2]) or (epoch == ARGV[2] and not greater(ARGV[3], sequence))) then return 0 end
redis.call('HSET', KEYS[2], 'epoch', ARGV[2], 'sequence', ARGV[3], 'data', ARGV[4])
redis.call('PEXPIRE', KEYS[2], ARGV[5])
return 1
`)

var loadRuntimeCheckpointScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return {-1} end
local data = redis.call('HGET', KEYS[2], 'data')
if not data then return {} end
return {data}
`)

var deleteRuntimeCheckpointScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
return redis.call('DEL', KEYS[2])
`)

func (coordinator *RedisCoordinator) SaveRuntimeCheckpoint(ctx context.Context, lease coordinationAbstract.RoomLease, checkpoint coordinationAbstract.RuntimeCheckpoint, ttl time.Duration) error {
	if err := coordinator.ensureOpen(); err != nil {
		return err
	}
	if lease.OwnerInstanceID != coordinator.instanceID || checkpoint.Epoch <= 0 || checkpoint.Sequence <= 0 || checkpoint.CapturedAt.IsZero() || !json.Valid(checkpoint.Payload) || len(checkpoint.Payload) > 256*1024 || ttl <= 0 {
		return errors.New("invalid runtime checkpoint")
	}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	result, err := saveRuntimeCheckpointScript.Run(ctx, coordinator.client, []string{roomLeaseKey(lease.RoomID), runtimeCheckpointKey(lease.RoomID)}, leaseValue(lease), checkpoint.Epoch, checkpoint.Sequence, payload, ttl.Milliseconds()).Int64()
	if err != nil {
		return unavailableError(err)
	}
	if result == -1 {
		return coordinationAbstract.ErrLeaseLost
	}
	if result == 0 {
		return coordinationAbstract.ErrCheckpointOrder
	}
	return nil
}

func (coordinator *RedisCoordinator) LoadRuntimeCheckpoint(ctx context.Context, lease coordinationAbstract.RoomLease) (coordinationAbstract.RuntimeCheckpoint, bool, error) {
	if err := coordinator.ensureOpen(); err != nil {
		return coordinationAbstract.RuntimeCheckpoint{}, false, err
	}
	if lease.OwnerInstanceID != coordinator.instanceID {
		return coordinationAbstract.RuntimeCheckpoint{}, false, coordinationAbstract.ErrLeaseLost
	}
	result, err := loadRuntimeCheckpointScript.Run(ctx, coordinator.client, []string{roomLeaseKey(lease.RoomID), runtimeCheckpointKey(lease.RoomID)}, leaseValue(lease)).Slice()
	if err != nil {
		return coordinationAbstract.RuntimeCheckpoint{}, false, unavailableError(err)
	}
	if len(result) == 0 {
		return coordinationAbstract.RuntimeCheckpoint{}, false, nil
	}
	data, ok := result[0].(string)
	if !ok {
		return coordinationAbstract.RuntimeCheckpoint{}, false, coordinationAbstract.ErrLeaseLost
	}
	var checkpoint coordinationAbstract.RuntimeCheckpoint
	err = json.Unmarshal([]byte(data), &checkpoint)
	return checkpoint, err == nil, err
}

func (coordinator *RedisCoordinator) DeleteRuntimeCheckpoint(ctx context.Context, lease coordinationAbstract.RoomLease) error {
	if err := coordinator.ensureOpen(); err != nil {
		return err
	}
	if lease.OwnerInstanceID != coordinator.instanceID {
		return coordinationAbstract.ErrLeaseLost
	}
	result, err := deleteRuntimeCheckpointScript.Run(ctx, coordinator.client, []string{roomLeaseKey(lease.RoomID), runtimeCheckpointKey(lease.RoomID)}, leaseValue(lease)).Int64()
	if err != nil {
		return unavailableError(err)
	}
	if result == -1 {
		return coordinationAbstract.ErrLeaseLost
	}
	return nil
}

var _ coordinationAbstract.RuntimeCheckpointStore = (*RedisCoordinator)(nil)
