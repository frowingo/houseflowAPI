package chore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
	"houseflowApi/internal/application/notification"
	notificationAbstract "houseflowApi/internal/application/notification/abstract"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	infrastructureJobs "houseflowApi/internal/infrastructure/jobs"
	"houseflowApi/internal/services"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// The fakes embed the repository interface and implement only what the
// notification service and the delivery jobs use.
type notifyFakeNotifications struct {
	databaseAbstract.DbRepository[entities.Notification]
	mutex  sync.Mutex
	stored map[primitive.ObjectID]entities.Notification
}

func newNotifyFakeNotifications() *notifyFakeNotifications {
	return &notifyFakeNotifications{stored: map[primitive.ObjectID]entities.Notification{}}
}

func (f *notifyFakeNotifications) Insert(_ context.Context, entity entities.Notification) (*entities.Notification, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	entity.Id = primitive.NewObjectID()
	f.stored[entity.Id] = entity
	return &entity, nil
}

func (f *notifyFakeNotifications) FindByID(_ context.Context, id primitive.ObjectID) (*entities.Notification, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	entity, ok := f.stored[id]
	if !ok {
		return nil, helpers.NewNotFoundError("database.error.document_not_found")
	}
	return &entity, nil
}

type notifyFakeHouses struct {
	databaseAbstract.DbRepository[entities.House]
	house entities.House
}

func (f notifyFakeHouses) FindByID(context.Context, primitive.ObjectID) (*entities.House, error) {
	return &f.house, nil
}

type notifyFakeUsers struct {
	databaseAbstract.DbRepository[entities.User]
	users []entities.User
}

func (f notifyFakeUsers) FindByID(_ context.Context, id primitive.ObjectID) (*entities.User, error) {
	for _, user := range f.users {
		if user.Id == id {
			return &user, nil
		}
	}
	return nil, helpers.NewNotFoundError("database.error.document_not_found")
}

type notifyFakeDeviceTokens struct {
	databaseAbstract.DbRepository[entities.DeviceToken]
	mutex   sync.Mutex
	tokens  []entities.DeviceToken
	deleted []primitive.ObjectID
}

func (f *notifyFakeDeviceTokens) FindManyByFilter(_ context.Context, filter bson.M, _ ...*options.FindOptions) ([]entities.DeviceToken, error) {
	userIDs := filter["userId"].(bson.M)["$in"].([]string)
	f.mutex.Lock()
	defer f.mutex.Unlock()
	var result []entities.DeviceToken
	for _, token := range f.tokens {
		for _, userID := range userIDs {
			if token.UserId == userID {
				result = append(result, token)
			}
		}
	}
	return result, nil
}

func (f *notifyFakeDeviceTokens) FindByID(_ context.Context, id primitive.ObjectID) (*entities.DeviceToken, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	for _, token := range f.tokens {
		if token.Id == id {
			return &token, nil
		}
	}
	return nil, helpers.NewNotFoundError("database.error.document_not_found")
}

func (f *notifyFakeDeviceTokens) Delete(_ context.Context, id primitive.ObjectID) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *notifyFakeDeviceTokens) deletedIDs() []primitive.ObjectID {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]primitive.ObjectID(nil), f.deleted...)
}

type notifyFakePush struct {
	mutex   sync.Mutex
	sent    map[string]notificationAbstract.PushMessage
	errors  map[string]error
	settled chan string
}

func newNotifyFakePush() *notifyFakePush {
	return &notifyFakePush{
		sent: map[string]notificationAbstract.PushMessage{}, errors: map[string]error{}, settled: make(chan string, 16),
	}
}

func (f *notifyFakePush) Send(_ context.Context, token string, message notificationAbstract.PushMessage) error {
	f.mutex.Lock()
	err := f.errors[token]
	if err == nil {
		f.sent[token] = message
	}
	f.mutex.Unlock()
	f.settled <- token
	return err
}

// waitFor waits until a send to each token was attempted.
func (f *notifyFakePush) waitFor(t *testing.T, tokens ...string) {
	t.Helper()
	pending := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		pending[token] = true
	}
	deadline := time.After(2 * time.Second)
	for len(pending) > 0 {
		select {
		case token := <-f.settled:
			delete(pending, token)
		case <-deadline:
			t.Fatalf("pushes not attempted: %v", pending)
		}
	}
}

type notifyFakeEvents struct {
	userIDs chan []string
}

func (f notifyFakeEvents) NotificationCreated(_ context.Context, _ string, userIDs []string) {
	f.userIDs <- userIDs
}

type notifyFakeLocalizer struct{}

func (notifyFakeLocalizer) LocalizeMessage(_ context.Context, language string, keyOrMessage string) string {
	return language + ":" + keyOrMessage
}

// notifyFakeQueue records what a job enqueues instead of running it.
type notifyFakeQueue struct {
	mutex   sync.Mutex
	jobs    []jobsAbstract.Job
	options []jobsAbstract.EnqueueOptions
	err     func(jobsAbstract.EnqueueOptions) error
}

func (f *notifyFakeQueue) Enqueue(_ context.Context, job jobsAbstract.Job, opts ...jobsAbstract.Option) error {
	resolved, err := jobsAbstract.ResolveOptions(opts...)
	if err != nil {
		return err
	}
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.jobs = append(f.jobs, job)
	f.options = append(f.options, resolved)
	if f.err != nil {
		return f.err(resolved)
	}
	return nil
}

const (
	notifySenderID = "650000000000000000000001"
	notifyMemberID = "650000000000000000000002"
	notifyOtherID  = "650000000000000000000003"
	notifyHouseID  = "650000000000000000000010"
)

// startNotifyRuntime runs the delivery jobs inline, as a process without
// Redis does; the delivery enqueues its push jobs on the same runtime.
func startNotifyRuntime(t *testing.T, newDelivery func(jobsAbstract.JobQueue) *notification.Delivery) *infrastructureJobs.InlineRuntime {
	t.Helper()
	runtime := infrastructureJobs.NewInlineRuntime()
	newDelivery(runtime).Register(runtime)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	return runtime
}

func TestNotifyStoresOnceAndPushesToHouseMembersButSender(t *testing.T) {
	invalidTokenID := primitive.NewObjectID()
	notifications := newNotifyFakeNotifications()
	deviceTokens := &notifyFakeDeviceTokens{tokens: []entities.DeviceToken{
		{Id: primitive.NewObjectID(), UserId: notifySenderID, Token: "sender-phone"},
		{Id: primitive.NewObjectID(), UserId: notifyMemberID, Token: "member-phone"},
		{Id: primitive.NewObjectID(), UserId: notifyMemberID, Token: "member-tablet"},
		{Id: invalidTokenID, UserId: notifyOtherID, Token: "other-old-phone"},
	}}
	users := notifyFakeUsers{users: []entities.User{
		{Id: notifyObjectID(notifyMemberID), Language: "tr"},
		{Id: notifyObjectID(notifyOtherID), Language: "en"},
	}}
	push := newNotifyFakePush()
	push.errors["other-old-phone"] = notificationAbstract.ErrInvalidPushToken
	runtime := startNotifyRuntime(t, func(queue jobsAbstract.JobQueue) *notification.Delivery {
		return notification.NewDelivery(notifications, deviceTokens, users, push, notifyFakeLocalizer{}, queue)
	})
	events := notifyFakeEvents{userIDs: make(chan []string, 1)}
	service := services.NewNotificationService("", services.NotificationDelivery{
		Notifications: notifications,
		Houses:        notifyFakeHouses{house: entities.House{MemberIds: []string{notifySenderID, notifyMemberID, notifyOtherID}}},
		Events:        events,
		Jobs:          runtime,
	})

	created, err := service.Notify(context.Background(), notificationAbstract.NotificationRequest{
		Type: entities.AnnouncementNotification, HouseID: notifyHouseID, SenderUserID: notifySenderID,
		Title: "Toplantı", Message: "Akşam 8'de",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications.stored) != 1 || len(created.RecipientUserIds) != 0 || created.ChoreId != nil {
		t.Fatalf("stored %+v", notifications.stored)
	}
	if !created.ExpireAt.After(created.CreatedAt.Add(29 * 24 * time.Hour)) {
		t.Fatalf("expireAt = %v", created.ExpireAt)
	}
	select {
	case userIDs := <-events.userIDs:
		if len(userIDs) != 2 || userIDs[0] != notifyMemberID || userIDs[1] != notifyOtherID {
			t.Fatalf("event recipients = %v", userIDs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no app event")
	}

	push.waitFor(t, "member-phone", "member-tablet", "other-old-phone")
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	push.mutex.Lock()
	defer push.mutex.Unlock()
	if _, ok := push.sent["sender-phone"]; ok {
		t.Fatal("sender received its own notification")
	}
	for _, token := range []string{"member-phone", "member-tablet"} {
		message := push.sent[token]
		if message.Title != "Toplantı" || message.Body != "Akşam 8'de" || message.Data["type"] != "announcement" ||
			message.Data["houseId"] != notifyHouseID || message.Data["notificationId"] != created.Id.Hex() {
			t.Fatalf("%s: unexpected push %+v", token, message)
		}
		if message.CollapseID != created.Id.Hex() || message.TTL != notification.PushTTL {
			t.Fatalf("%s: collapse %q ttl %s", token, message.CollapseID, message.TTL)
		}
	}
	if deleted := deviceTokens.deletedIDs(); len(deleted) != 1 || deleted[0] != invalidTokenID {
		t.Fatalf("deleted tokens = %v", deleted)
	}
}

func TestNotifyRendersLocalizedPushInRecipientLanguage(t *testing.T) {
	notifications := newNotifyFakeNotifications()
	deviceTokens := &notifyFakeDeviceTokens{tokens: []entities.DeviceToken{
		{Id: primitive.NewObjectID(), UserId: notifyMemberID, Token: "member-phone"},
	}}
	users := notifyFakeUsers{users: []entities.User{{Id: notifyObjectID(notifyMemberID), Language: "tr-TR"}}}
	push := newNotifyFakePush()
	runtime := startNotifyRuntime(t, func(queue jobsAbstract.JobQueue) *notification.Delivery {
		return notification.NewDelivery(notifications, deviceTokens, users, push, notifyFakeLocalizer{}, queue)
	})
	service := services.NewNotificationService("", services.NotificationDelivery{Notifications: notifications, Jobs: runtime})

	created, err := service.Notify(context.Background(), notificationAbstract.NotificationRequest{
		Type: entities.ChoreReminderNotification, HouseID: notifyHouseID, SenderUserID: notifySenderID,
		RecipientUserIDs: []string{notifyMemberID}, ChoreID: "chore-1",
		Title: "notification.chore_reminder.title", Message: "notification.chore_reminder.message",
		Localized: true, Args: []string{"Ayşe", "Yıka||Kurula; Kaldır"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ChoreId == nil || *created.ChoreId != "chore-1" {
		t.Fatalf("choreId = %v", created.ChoreId)
	}
	push.waitFor(t, "member-phone")
	push.mutex.Lock()
	defer push.mutex.Unlock()
	message := push.sent["member-phone"]
	// "||" and "; " in user text are defused (the latter with a no-break space).
	if message.Title != "tr:notification.chore_reminder.title||Ayşe||Yıka|Kurula;\u00a0Kaldır" {
		t.Fatalf("title = %q", message.Title)
	}
	if message.Data["choreId"] != "chore-1" {
		t.Fatalf("data = %v", message.Data)
	}
}

func TestNotifyStillStoresWhenPushCannotBeQueued(t *testing.T) {
	notifications := newNotifyFakeNotifications()
	queue := &notifyFakeQueue{err: func(jobsAbstract.EnqueueOptions) error { return errors.New("redis down") }}
	service := services.NewNotificationService("", services.NotificationDelivery{Notifications: notifications, Jobs: queue})
	_, err := service.Notify(context.Background(), notificationAbstract.NotificationRequest{
		Type: entities.AnnouncementNotification, HouseID: notifyHouseID, SenderUserID: notifySenderID,
		RecipientUserIDs: []string{notifyMemberID}, Title: "t", Message: "m",
	})
	if err != nil || len(notifications.stored) != 1 {
		t.Fatalf("err = %v, stored = %d", err, len(notifications.stored))
	}
	if len(queue.jobs) != 1 || queue.jobs[0].Type != notification.FanoutJobType {
		t.Fatalf("jobs = %+v", queue.jobs)
	}
}

func TestFanoutQueuesOnePushPerDeviceWithIDsOnly(t *testing.T) {
	notificationID := primitive.NewObjectID().Hex()
	phoneID, tabletID := primitive.NewObjectID(), primitive.NewObjectID()
	deviceTokens := &notifyFakeDeviceTokens{tokens: []entities.DeviceToken{
		{Id: phoneID, UserId: notifyMemberID, Token: "secret-phone-token"},
		{Id: tabletID, UserId: notifyMemberID, Token: "secret-tablet-token"},
		{Id: primitive.NewObjectID(), UserId: notifyOtherID, Token: "not-a-recipient"},
	}}
	// A push already queued by an earlier attempt of the fanout is skipped.
	queue := &notifyFakeQueue{err: func(options jobsAbstract.EnqueueOptions) error {
		if options.TaskID == notification.PushTaskID(notificationID, phoneID.Hex()) {
			return jobsAbstract.ErrDuplicate
		}
		return nil
	}}
	delivery := notification.NewDelivery(newNotifyFakeNotifications(), deviceTokens, notifyFakeUsers{}, newNotifyFakePush(), nil, queue)

	payload, _ := json.Marshal(notification.FanoutPayload{NotificationID: notificationID, RecipientUserIDs: []string{notifyMemberID}})
	if err := delivery.RunFanout(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if len(queue.jobs) != 2 {
		t.Fatalf("queued %d pushes", len(queue.jobs))
	}
	for index, job := range queue.jobs {
		if job.Type != notification.PushJobType || strings.Contains(string(job.Payload), "secret") {
			t.Fatalf("job %d: %s %s", index, job.Type, job.Payload)
		}
		push, err := jobsAbstract.DecodeJSON[notification.PushPayload](job.Payload)
		if err != nil || push.NotificationID != notificationID || push.UserID != notifyMemberID {
			t.Fatalf("job %d payload %+v: %v", index, push, err)
		}
		options := queue.options[index]
		if options.TaskID != notification.PushTaskID(notificationID, push.DeviceTokenID) || options.Retention <= 0 ||
			options.Queue != jobsAbstract.QueueDefault {
			t.Fatalf("job %d options %+v", index, options)
		}
	}

	// Any other enqueue failure fails the fanout, so it is retried.
	queue.err = func(jobsAbstract.EnqueueOptions) error { return errors.New("redis down") }
	if err := delivery.RunFanout(context.Background(), payload); err == nil {
		t.Fatal("fanout ignored an enqueue failure")
	}
}

func TestPushJobDropsWhatIsNoLongerDeliverable(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	notifications := newNotifyFakeNotifications()
	fresh, _ := notifications.Insert(context.Background(), entities.Notification{Type: entities.AnnouncementNotification, CreatedAt: now.Add(-time.Hour)})
	stale, _ := notifications.Insert(context.Background(), entities.Notification{Type: entities.AnnouncementNotification, CreatedAt: now.Add(-25 * time.Hour)})
	tokenID := primitive.NewObjectID()
	deviceTokens := &notifyFakeDeviceTokens{tokens: []entities.DeviceToken{{Id: tokenID, UserId: notifyMemberID, Token: "phone"}}}
	push := newNotifyFakePush()
	delivery := notification.NewDelivery(notifications, deviceTokens, notifyFakeUsers{}, push, nil, nil,
		notification.WithDeliveryClock(func() time.Time { return now }))

	cases := map[string]notification.PushPayload{
		"older than the push TTL":    {NotificationID: stale.Id.Hex(), DeviceTokenID: tokenID.Hex(), UserID: notifyMemberID},
		"notification deleted":       {NotificationID: primitive.NewObjectID().Hex(), DeviceTokenID: tokenID.Hex(), UserID: notifyMemberID},
		"device token removed":       {NotificationID: fresh.Id.Hex(), DeviceTokenID: primitive.NewObjectID().Hex(), UserID: notifyMemberID},
		"device moved to a new user": {NotificationID: fresh.Id.Hex(), DeviceTokenID: tokenID.Hex(), UserID: notifyOtherID},
	}
	for name, payload := range cases {
		body, _ := json.Marshal(payload)
		if err := delivery.RunPush(context.Background(), body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(push.sent) != 0 || len(push.settled) != 0 {
		t.Fatalf("sent %v", push.sent)
	}

	bad, _ := json.Marshal(notification.PushPayload{NotificationID: "not-an-id", DeviceTokenID: tokenID.Hex()})
	if err := delivery.RunPush(context.Background(), bad); !errors.Is(err, jobsAbstract.ErrPermanent) {
		t.Fatalf("bad id: err = %v", err)
	}
}

func TestPushJobMapsSendFailuresToRetries(t *testing.T) {
	notifications := newNotifyFakeNotifications()
	created, _ := notifications.Insert(context.Background(), entities.Notification{Type: entities.AnnouncementNotification, CreatedAt: time.Now()})
	tokenID := primitive.NewObjectID()
	deviceTokens := &notifyFakeDeviceTokens{tokens: []entities.DeviceToken{{Id: tokenID, UserId: notifyMemberID, Token: "phone"}}}
	payload, _ := json.Marshal(notification.PushPayload{NotificationID: created.Id.Hex(), DeviceTokenID: tokenID.Hex(), UserID: notifyMemberID})

	run := func(sendErr error) error {
		push := newNotifyFakePush()
		push.errors["phone"] = sendErr
		return notification.NewDelivery(notifications, deviceTokens, notifyFakeUsers{}, push, nil, nil).
			RunPush(context.Background(), payload)
	}

	if err := run(rejectedSend(notificationAbstract.ErrPushRejected)); !errors.Is(err, jobsAbstract.ErrPermanent) {
		t.Fatalf("rejected: err = %v", err)
	}
	var retryAfter *jobsAbstract.RetryAfterError
	err := run(&notificationAbstract.PushThrottledError{RetryAfter: 42 * time.Second, Err: errors.New("429")})
	if !errors.As(err, &retryAfter) || retryAfter.Delay != 42*time.Second || errors.Is(err, jobsAbstract.ErrPermanent) {
		t.Fatalf("throttled: err = %v", err)
	}
	transient := errors.New("connection reset")
	if err := run(transient); !errors.Is(err, transient) || errors.Is(err, jobsAbstract.ErrPermanent) {
		t.Fatalf("transient: err = %v", err)
	}
	if len(deviceTokens.deletedIDs()) != 0 {
		t.Fatal("a retryable failure removed the device token")
	}
	if err := run(notificationAbstract.ErrInvalidPushToken); err != nil {
		t.Fatalf("invalid token: err = %v", err)
	}
	if deleted := deviceTokens.deletedIDs(); len(deleted) != 1 || deleted[0] != tokenID {
		t.Fatalf("deleted = %v", deleted)
	}
}

func TestEnqueueFanoutSkipsEmptyAndDuplicateFanouts(t *testing.T) {
	queue := &notifyFakeQueue{}
	if err := notification.EnqueueFanout(context.Background(), queue, "n1", nil); err != nil || len(queue.jobs) != 0 {
		t.Fatalf("empty: err = %v, jobs = %d", err, len(queue.jobs))
	}
	queue.err = func(jobsAbstract.EnqueueOptions) error { return jobsAbstract.ErrDuplicate }
	if err := notification.EnqueueFanout(context.Background(), queue, "n1", []string{notifyMemberID}); err != nil {
		t.Fatalf("duplicate: err = %v", err)
	}
	if queue.options[0].TaskID != jobsAbstract.TypePrefix+"notification:fanout:n1" {
		t.Fatalf("task id = %q", queue.options[0].TaskID)
	}
}

func rejectedSend(err error) error {
	return errors.Join(errors.New("fcm send failed: 400 INVALID_ARGUMENT"), err)
}

func notifyObjectID(hex string) primitive.ObjectID {
	id, err := primitive.ObjectIDFromHex(hex)
	if err != nil {
		panic(err)
	}
	return id
}
