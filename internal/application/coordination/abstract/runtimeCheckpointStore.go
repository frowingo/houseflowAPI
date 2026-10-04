package abstract

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrCheckpointOrder = errors.New("runtime checkpoint is older than the stored checkpoint")

// RuntimeCheckpoint contains opaque game-owned state. The store atomically
// validates the Redis lease and epoch/sequence; it never interprets physics.
type RuntimeCheckpoint struct {
	Epoch      int64           `json:"epoch"`
	Sequence   int64           `json:"sequence"`
	CapturedAt time.Time       `json:"capturedAt"`
	Payload    json.RawMessage `json:"payload"`
}

type RuntimeCheckpointStore interface {
	SaveRuntimeCheckpoint(ctx context.Context, lease RoomLease, checkpoint RuntimeCheckpoint, ttl time.Duration) error
	LoadRuntimeCheckpoint(ctx context.Context, lease RoomLease) (RuntimeCheckpoint, bool, error)
	DeleteRuntimeCheckpoint(ctx context.Context, lease RoomLease) error
}
