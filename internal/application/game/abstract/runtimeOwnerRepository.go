package abstract

import (
	"context"
	"errors"
)

var (
	ErrRuntimeOwnerConflict    = errors.New("runtime ownership generation changed")
	ErrRuntimeRecoveryRequired = errors.New("started game requires checkpoint recovery")
)

// RuntimeOwner is durable fencing state, not a replacement for the Redis lease.
type RuntimeOwner struct {
	SessionID       string
	Generation      int64
	OwnerInstanceID string
	LeaseID         string
	Started         bool
}

type RuntimeOwnerRepository interface {
	FindRuntimeOwner(ctx context.Context, sessionID string) (RuntimeOwner, error)
	ClaimRuntimeOwner(ctx context.Context, expectedGeneration int64, owner RuntimeOwner) (RuntimeOwner, error)
	MarkRuntimeStarted(ctx context.Context, owner RuntimeOwner) error
}
