package abstract

import "context"

// GameConfigBus tells every instance that a game's stored config changed.
// Delivery is best effort: instances also reload on a fixed interval.
type GameConfigBus interface {
	PublishGameConfigChanged(ctx context.Context, gameKey string) error
	SubscribeGameConfigChanges(ctx context.Context) (RoomEventSubscription, error)
}
