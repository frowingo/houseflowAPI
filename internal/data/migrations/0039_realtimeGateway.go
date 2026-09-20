package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type realtimeGateway struct{}

func (m *realtimeGateway) Version() string { return "0039" }
func (m *realtimeGateway) Name() string    { return "realtimeGateway" }

func (m *realtimeGateway) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "realtime.error.unavailable", "Realtime game service is temporarily unavailable."},
		{"tr", "realtime.error.unavailable", "Anlık oyun servisi geçici olarak kullanılamıyor."},
		{"en", "realtime.error.invalid_command", "The realtime command is invalid."},
		{"tr", "realtime.error.invalid_command", "Anlık oyun komutu geçersiz."},
		{"en", "realtime.error.unsupported_protocol", "The realtime protocol version is not supported."},
		{"tr", "realtime.error.unsupported_protocol", "Anlık oyun protokolü sürümü desteklenmiyor."},
		{"en", "realtime.error.rate_limited", "Too many realtime commands were sent."},
		{"tr", "realtime.error.rate_limited", "Çok fazla anlık oyun komutu gönderildi."},
		{"en", "realtime.error.command_failed", "The realtime command could not be completed."},
		{"tr", "realtime.error.command_failed", "Anlık oyun komutu tamamlanamadı."},
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
