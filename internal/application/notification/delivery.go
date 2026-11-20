package notification

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
	notificationAbstract "houseflowApi/internal/application/notification/abstract"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"

	"go.mongodb.org/mongo-driver/bson"
)

// Job types of push delivery. A stored notification is fanned out to one push
// job per device token, so a slow or failing device retries on its own.
const (
	FanoutJobType = "fauscrew:notification:fanout"
	PushJobType   = "fauscrew:push:send"
)

// The payloads' wire format. They carry ids only, never a device token or
// text: a job is read again from the database when it runs. Field names are
// stable because during a rolling deploy one version runs the jobs of another.
type FanoutPayload struct {
	NotificationID   string   `json:"notificationId"`
	RecipientUserIDs []string `json:"recipientUserIds"`
}

type PushPayload struct {
	NotificationID string `json:"notificationId"`
	DeviceTokenID  string `json:"deviceTokenId"`
	UserID         string `json:"userId"`
}

const (
	fanoutMaxRetry = 5
	fanoutTimeout  = 30 * time.Second
	// Retries run 5s, 10s, ... about 5 minutes in all.
	pushMaxRetry = 6
	// Above the FCM request deadline (10s), so that fires first.
	pushTimeout = 20 * time.Second
	// A completed push stays known this long, so a retried fanout does not
	// send it again.
	pushRetention = time.Hour
	// PushTTL is how long a push is worth delivering: FCM keeps trying an
	// offline device this long, and a job still retrying after it is dropped.
	PushTTL = 24 * time.Hour
)

// EnqueueFanout queues the push delivery of a stored notification.
func EnqueueFanout(ctx context.Context, queue jobsAbstract.JobQueue, notificationID string, recipientUserIDs []string) error {
	if len(recipientUserIDs) == 0 {
		return nil
	}
	job, err := jobsAbstract.NewJSONJob(FanoutJobType, FanoutPayload{
		NotificationID: notificationID, RecipientUserIDs: recipientUserIDs,
	})
	if err != nil {
		return err
	}
	err = queue.Enqueue(ctx, job,
		jobsAbstract.InQueue(jobsAbstract.QueueDefault),
		jobsAbstract.MaxRetry(fanoutMaxRetry),
		jobsAbstract.Timeout(fanoutTimeout),
		jobsAbstract.TaskID(jobsAbstract.TypePrefix+"notification:fanout:"+notificationID),
	)
	if errors.Is(err, jobsAbstract.ErrDuplicate) {
		return nil
	}
	return err
}

// Delivery runs the fanout and push jobs.
type Delivery struct {
	notifications databaseAbstract.DbRepository[entities.Notification]
	deviceTokens  databaseAbstract.DbRepository[entities.DeviceToken]
	users         databaseAbstract.DbRepository[entities.User]
	push          notificationAbstract.PushSender
	localizer     helpers.MessageLocalizer
	queue         jobsAbstract.JobQueue
	now           func() time.Time
}

type DeliveryOption func(*Delivery)

// WithDeliveryClock replaces the clock that decides when a push is too old.
func WithDeliveryClock(now func() time.Time) DeliveryOption {
	return func(delivery *Delivery) { delivery.now = now }
}

func NewDelivery(
	notifications databaseAbstract.DbRepository[entities.Notification],
	deviceTokens databaseAbstract.DbRepository[entities.DeviceToken],
	users databaseAbstract.DbRepository[entities.User],
	push notificationAbstract.PushSender,
	localizer helpers.MessageLocalizer,
	queue jobsAbstract.JobQueue,
	options ...DeliveryOption,
) *Delivery {
	delivery := &Delivery{
		notifications: notifications, deviceTokens: deviceTokens, users: users,
		push: push, localizer: localizer, queue: queue, now: time.Now,
	}
	for _, option := range options {
		option(delivery)
	}
	return delivery
}

// Register binds the fanout and push jobs; call it before jobs start.
func (d *Delivery) Register(registry jobsAbstract.HandlerRegistry) {
	registry.Register(FanoutJobType, d.RunFanout)
	registry.Register(PushJobType, d.RunPush)
}

// RunFanout queues one push job per device of the recipients. A failed
// enqueue fails the job; on its retry the pushes already queued or sent are
// skipped by their task id.
func (d *Delivery) RunFanout(ctx context.Context, payload []byte) error {
	fanout, err := jobsAbstract.DecodeJSON[FanoutPayload](payload)
	if err != nil {
		return err
	}
	if fanout.NotificationID == "" || len(fanout.RecipientUserIDs) == 0 {
		return nil
	}
	tokens, err := d.deviceTokens.FindManyByFilter(ctx, bson.M{"userId": bson.M{"$in": fanout.RecipientUserIDs}})
	if err != nil {
		return err
	}
	for _, token := range tokens {
		job, err := jobsAbstract.NewJSONJob(PushJobType, PushPayload{
			NotificationID: fanout.NotificationID, DeviceTokenID: token.Id.Hex(), UserID: token.UserId,
		})
		if err != nil {
			return err
		}
		err = d.queue.Enqueue(ctx, job,
			jobsAbstract.InQueue(jobsAbstract.QueueDefault),
			jobsAbstract.MaxRetry(pushMaxRetry),
			jobsAbstract.Timeout(pushTimeout),
			jobsAbstract.TaskID(PushTaskID(fanout.NotificationID, token.Id.Hex())),
			jobsAbstract.Retention(pushRetention),
		)
		if err != nil && !errors.Is(err, jobsAbstract.ErrDuplicate) {
			return fmt.Errorf("queue push for device %s: %w", token.Id.Hex(), err)
		}
	}
	return nil
}

// PushTaskID is the task id of the push of a notification to a device.
func PushTaskID(notificationID, deviceTokenID string) string {
	return jobsAbstract.TypePrefix + "push:" + notificationID + ":" + deviceTokenID
}

// RunPush sends a notification to a device. A notification or device that is
// gone, a device that moved to another account and a push older than PushTTL
// are dropped; a token FCM no longer accepts is removed.
func (d *Delivery) RunPush(ctx context.Context, payload []byte) error {
	job, err := jobsAbstract.DecodeJSON[PushPayload](payload)
	if err != nil {
		return err
	}
	notificationID, err := helpers.ToMongoId(job.NotificationID)
	if err != nil {
		return fmt.Errorf("%w: notification id: %v", jobsAbstract.ErrPermanent, err)
	}
	tokenID, err := helpers.ToMongoId(job.DeviceTokenID)
	if err != nil {
		return fmt.Errorf("%w: device token id: %v", jobsAbstract.ErrPermanent, err)
	}

	stored, err := d.notifications.FindByID(ctx, notificationID)
	if notFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.now().Sub(stored.CreatedAt) > PushTTL {
		return nil
	}
	token, err := d.deviceTokens.FindByID(ctx, tokenID)
	if notFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if token.UserId != job.UserID {
		return nil
	}

	language, err := d.language(ctx, job.UserID)
	if err != nil {
		return err
	}
	title, body := Render(ctx, d.localizer, language, *stored)
	data := map[string]string{
		"notificationId": job.NotificationID,
		"type":           string(stored.Type),
		"houseId":        stored.HouseId,
	}
	if stored.ChoreId != nil {
		data["choreId"] = *stored.ChoreId
	}
	err = d.push.Send(ctx, token.Token, notificationAbstract.PushMessage{
		Title: title, Body: body, Data: data, CollapseID: job.NotificationID, TTL: PushTTL,
	})

	var throttled *notificationAbstract.PushThrottledError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, notificationAbstract.ErrInvalidPushToken):
		if err := d.deviceTokens.Delete(ctx, token.Id); err != nil && !notFound(err) {
			log.Printf("push: removing device token %s failed: %v", job.DeviceTokenID, err)
		}
		return nil
	case errors.Is(err, notificationAbstract.ErrPushRejected):
		return fmt.Errorf("%w: %w", jobsAbstract.ErrPermanent, err)
	case errors.As(err, &throttled):
		return jobsAbstract.RetryAfter(throttled.RetryAfter, err)
	default:
		return err
	}
}

// language is the reader's language, or the default one when the account is
// gone (the push is then dropped with its device token by account removal).
func (d *Delivery) language(ctx context.Context, userID string) (string, error) {
	if d.users == nil {
		return helpers.DefaultLanguage, nil
	}
	id, err := helpers.ToMongoId(userID)
	if err != nil {
		return helpers.DefaultLanguage, nil
	}
	user, err := d.users.FindByID(ctx, id)
	if notFound(err) {
		return helpers.DefaultLanguage, nil
	}
	if err != nil {
		return "", err
	}
	return helpers.NormalizeLanguage(user.Language), nil
}

// notFound reports a record that is gone; its job has nothing left to do.
func notFound(err error) bool {
	var applicationError *helpers.ApplicationError
	return errors.As(err, &applicationError) && applicationError.Kind == helpers.ErrorKindNotFound
}
