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
	SessionID         string
	Generation        int64
	OwnerInstanceID   string
	LeaseID           string
	Started           bool
	CompletionTrigger *CommandDescriptor
}

type RuntimeOwnerRepository interface {
	FindRuntimeOwner(ctx context.Context, sessionID string) (RuntimeOwner, error)
	ClaimRuntimeOwner(ctx context.Context, expectedGeneration int64, owner RuntimeOwner) (RuntimeOwner, error)
	MarkRuntimeStarted(ctx context.Context, owner RuntimeOwner) error
	SaveCompletionTrigger(ctx context.Context, owner RuntimeOwner, command CommandDescriptor) error
}
