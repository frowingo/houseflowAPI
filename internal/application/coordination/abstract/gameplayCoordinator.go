package abstract

import "context"

// GameplayCoordinator is best-effort and ephemeral. Steering is never replayed
// from a durable command stream; control grants/ACKs confirm actual handling.
type GameplayCoordinator interface {
	PublishGameplay(ctx context.Context, targetInstanceID string, envelope MessageEnvelope) error
	SubscribeGameplay(ctx context.Context) (RoomEventSubscription, error)
}
