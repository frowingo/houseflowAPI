package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	deviceTokenCollection        = "DeviceToken"
	deviceTokenStaleAfterSeconds = 270 * 24 * 60 * 60
)

// notificationDelivery prepares the house notification panel, push device
// tokens and chore reminders. A chore reminder is sent once per sender:
// the partial unique index rejects a second one, even under concurrency.
type notificationDelivery struct{}

func (m *notificationDelivery) Version() string { return "0070" }
func (m *notificationDelivery) Name() string    { return "notificationDelivery" }

func (m *notificationDelivery) Up(ctx context.Context, db *mongo.Database) error {
	if _, err := db.Collection("Notification").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			// The panel pages newest first with an _id cursor.
			Keys:    bson.D{{Key: "houseId", Value: 1}, {Key: "_id", Value: -1}},
			Options: options.Index().SetName("idxNotificationHouseNewest"),
		},
		{
			Keys: bson.D{{Key: "choreId", Value: 1}, {Key: "senderUserId", Value: 1}},
			Options: options.Index().SetName("uqNotificationChoreReminder").SetUnique(true).
				SetPartialFilterExpression(bson.M{"type": "chore_reminder"}),
		},
		{
			Keys:    bson.D{{Key: "expireAt", Value: 1}},
			Options: options.Index().SetName("ttlNotificationExpireAt").SetExpireAfterSeconds(0),
		},
	}); err != nil {
		return err
	}

	validator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{"userId", "token", "platform", "createdOn", "updatedOn"},
		"properties": bson.M{
			"userId":    bson.M{"bsonType": "string", "minLength": 1},
			"token":     bson.M{"bsonType": "string", "minLength": 1},
			"platform":  bson.M{"enum": bson.A{"ios", "android"}},
			"createdOn": bson.M{"bsonType": "date"},
			"updatedOn": bson.M{"bsonType": "date"},
		},
	}}
	err := db.CreateCollection(ctx, deviceTokenCollection, options.CreateCollection().SetValidator(validator))
	if err != nil && !isCollectionExistsError(err) {
		return err
	}
	if _, err := db.Collection(deviceTokenCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "token", Value: 1}},
			Options: options.Index().SetName("uqDeviceTokenToken").SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "userId", Value: 1}},
			Options: options.Index().SetName("idxDeviceTokenUserId"),
		},
		{
			// The app registers its token on every start, which refreshes
			// updatedOn; a token silent this long is stale (FCM treats 270
			// days of inactivity as expired) and is removed by Mongo.
			Keys:    bson.D{{Key: "updatedOn", Value: 1}},
			Options: options.Index().SetName("ttlDeviceTokenUpdatedOn").SetExpireAfterSeconds(deviceTokenStaleAfterSeconds),
		},
	}); err != nil {
		return err
	}

	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "notification.error.invalid_device_token", "The device token is not valid."},
		{"tr", "notification.error.invalid_device_token", "Cihaz anahtarı geçersiz."},
		{"en", "notification.error.invalid_platform", "The device platform must be ios or android."},
		{"tr", "notification.error.invalid_platform", "Cihaz platformu ios ya da android olmalı."},
		{"en", "notification.error.failed_register_device", "The device could not be registered for notifications."},
		{"tr", "notification.error.failed_register_device", "Cihaz bildirimler için kaydedilemedi."},
		{"en", "notification.error.failed_create", "The notification could not be created."},
		{"tr", "notification.error.failed_create", "Bildirim oluşturulamadı."},
		{"en", "notification.error.failed_list", "Notifications could not be loaded."},
		{"tr", "notification.error.failed_list", "Bildirimler yüklenemedi."},
		{"en", "notification.error.invalid_cursor", "The notification cursor is not valid."},
		{"tr", "notification.error.invalid_cursor", "Bildirim imleci geçersiz."},
		{"en", "chore.reminder.error.no_assignee", "This chore is not assigned to anyone."},
		{"tr", "chore.reminder.error.no_assignee", "Bu iş kimseye atanmamış."},
		{"en", "chore.reminder.error.self_reminder", "You cannot send a reminder for a chore assigned to you."},
		{"tr", "chore.reminder.error.self_reminder", "Sana atanmış bir iş için hatırlatma gönderemezsin."},
		{"en", "chore.reminder.error.invalid_status", "A reminder can only be sent for a chore that is not started or in progress."},
		{"tr", "chore.reminder.error.invalid_status", "Hatırlatma yalnızca başlamamış ya da devam eden bir iş için gönderilebilir."},
		{"en", "chore.reminder.error.outside_window", "A reminder can only be sent in the last 12 hours before the due date."},
		{"tr", "chore.reminder.error.outside_window", "Hatırlatma yalnızca son yapım tarihinden önceki 12 saat içinde gönderilebilir."},
		{"en", "chore.reminder.error.already_sent", "You have already sent a reminder for this chore."},
		{"tr", "chore.reminder.error.already_sent", "Bu iş için zaten hatırlatma gönderdin."},
		{"en", "notification.chore_reminder.title", "Reminder: {1}"},
		{"tr", "notification.chore_reminder.title", "Hatırlatma: {1}"},
		{"en", "notification.chore_reminder.message", "{0} reminds you to finish \"{1}\" before it is due."},
		{"tr", "notification.chore_reminder.message", "{0}, \"{1}\" işini süresi dolmadan yapmanı hatırlatıyor."},
	}
	for _, message := range messages {
		if _, err := db.Collection("localization").UpdateOne(ctx, bson.M{
			"language": message.language,
			"type":     "message",
			"key":      message.key,
		}, bson.M{"$setOnInsert": bson.M{
			"value":     message.value,
			"createdOn": now,
			"updatedOn": now,
		}}, options.Update().SetUpsert(true)); err != nil {
			return err
		}
	}
	return nil
}
