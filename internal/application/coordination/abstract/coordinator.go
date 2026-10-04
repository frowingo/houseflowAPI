package abstract

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrUnavailable        = errors.New("coordination service is unavailable")
	ErrLeaseLost          = errors.New("room ownership lease is no longer valid")
	ErrAlreadySubscribed  = errors.New("instance command subscription is already active")
	ErrCoordinatorStopped = errors.New("coordination service is stopped")
)

type RoomLease struct {
	RoomID          string
	OwnerInstanceID string
	LeaseID         string
	FencingToken    int64
	ExpiresAt       time.Time
}

type MessageEnvelope struct {
	MessageID        string          `json:"messageId"`
	RoomID           string          `json:"roomId"`
	Type             string          `json:"type"`
	ActorID          string          `json:"actorId,omitempty"`
	ConnectionID     string          `json:"connectionId,omitempty"`
	SourceInstanceID string          `json:"sourceInstanceId"`
	FencingToken     int64           `json:"fencingToken,omitempty"`
	Sequence         int64           `json:"sequence,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	Payload          json.RawMessage `json:"payload,omitempty"`
}

type Presence struct {
	RoomID       string    `json:"roomId"`
	UserID       string    `json:"userId"`
	ConnectionID string    `json:"connectionId"`
	InstanceID   string    `json:"instanceId"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// CommandDelivery is backed by a durable Redis stream entry. A command must
// only be acknowledged after the application has handled it successfully.
type CommandDelivery interface {
	Envelope() MessageEnvelope
	Ack(ctx context.Context) error
}

type CommandSubscription interface {
	Deliveries() <-chan CommandDelivery
	Errors() <-chan error
	Close() error
}

type RoomEventSubscription interface {
	Messages() <-chan MessageEnvelope
	Errors() <-chan error
	Close() error
}

// Coordinator contains only cross-instance, short-lived coordination state.
// Permanent game/session state remains outside this contract.
type Coordinator interface {
	InstanceID() string
	Start(ctx context.Context)
	Available() bool
	Ping(ctx context.Context) error
	InstanceAlive(ctx context.Context, instanceID string) (bool, error)

	AcquireRoom(ctx context.Context, roomID string, ttl time.Duration) (RoomLease, bool, error)
	RenewRoom(ctx context.Context, lease RoomLease, ttl time.Duration) (bool, error)
	ReleaseRoom(ctx context.Context, lease RoomLease) (bool, error)
	CurrentRoomOwner(ctx context.Context, roomID string) (RoomLease, bool, error)

	PublishCommand(ctx context.Context, targetInstanceID string, envelope MessageEnvelope) error
	SubscribeCommands(ctx context.Context) (CommandSubscription, error)
	PublishRoomEvent(ctx context.Context, lease RoomLease, envelope MessageEnvelope) error
	SubscribeRoomEvents(ctx context.Context, roomID string) (RoomEventSubscription, error)
	ClaimMessage(ctx context.Context, scopeID string, messageID string, ttl time.Duration) (bool, error)

	TouchPresence(ctx context.Context, presence Presence, ttl time.Duration) error
	RemovePresence(ctx context.Context, roomID string, connectionID string) error
	ListPresence(ctx context.Context, roomID string) ([]Presence, error)

	Close(ctx context.Context) error
}
