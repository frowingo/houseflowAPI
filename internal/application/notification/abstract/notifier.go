package abstract

import (
	"context"
	"errors"
	"fmt"
	"time"

	"houseflowApi/internal/data/entities"
)

// NotificationRequest describes a house notification. RecipientUserIDs
// narrows it to some members; empty sends it to every member but the sender.
// When Localized is set, Title and Message are localization keys rendered
// with Args in each reader's language.
type NotificationRequest struct {
	Type             entities.NotificationType
	HouseID          string
	SenderUserID     string
	RecipientUserIDs []string
	ChoreID          string
	Title            string
	Message          string
	Localized        bool
	Args             []string
}

// Notifier stores a notification and delivers it to the recipients' open
// app sessions and devices. A storage failure fails the call and is returned
// as is, so a duplicate key can be told apart; delivery is best effort.
type Notifier interface {
	Notify(ctx context.Context, request NotificationRequest) (*entities.Notification, error)
}

type PushMessage struct {
	Title string
	Body  string
	Data  map[string]string
	// CollapseID makes a device show one notification for repeated sends
	// with the same ID, e.g. when a send is retried after a timeout.
	CollapseID string
	// TTL is how long the provider keeps trying to reach an offline device.
	TTL time.Duration
}

var (
	// ErrInvalidPushToken reports a device token the push provider no
	// longer accepts; the token is removed.
	ErrInvalidPushToken = errors.New("push token is no longer valid")
	// ErrPushRejected reports a message the provider refuses for a reason a
	// retry cannot fix (bad payload, wrong project).
	ErrPushRejected = errors.New("push message was rejected")
)

// PushThrottledError reports that the provider asked to wait (HTTP 429 or
// 503 with Retry-After) before sending again.
type PushThrottledError struct {
	RetryAfter time.Duration
	Err        error
}

func (e *PushThrottledError) Error() string {
	return fmt.Sprintf("push throttled, retry after %s: %v", e.RetryAfter, e.Err)
}

func (e *PushThrottledError) Unwrap() error { return e.Err }

type PushSender interface {
	Send(ctx context.Context, token string, message PushMessage) error
}
