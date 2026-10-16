package abstract

import "context"

const (
	HouseChangedEvent = "house.changed"
	UserChangedEvent  = "user.changed"
)

// AppEvent is an invalidation signal. UserIDs are routing metadata and are
// never sent to a WebSocket client.
type AppEvent struct {
	Type    string   `json:"type"`
	HouseID string   `json:"houseId,omitempty"`
	UserIDs []string `json:"userIds"`
}

type AppEventSubscription interface {
	Events() <-chan AppEvent
	Errors() <-chan error
	Close() error
}

type AppEventBus interface {
	Available() bool
	PublishAppEvent(context.Context, AppEvent) error
	SubscribeAppEvents(context.Context) (AppEventSubscription, error)
}
