package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type userInfoHistoryLocalizations struct{}

func (m *userInfoHistoryLocalizations) Version() string { return "0030" }
func (m *userInfoHistoryLocalizations) Name() string    { return "userInfoHistoryLocalizations" }

func (m *userInfoHistoryLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now()
	messages := []struct {
		language string
		value    string
	}{
		{language: "en", value: "%s can only be updated once every 20 days."},
		{language: "tr", value: "%s alanı yalnızca 20 günde bir güncellenebilir."},
	}

	for _, message := range messages {
		_, err := db.Collection("localization").UpdateOne(
			ctx,
			bson.M{
				"language": message.language,
				"type":     "message",
				"key":      "user.error.profile_field_update_limit",
			},
			bson.M{"$setOnInsert": bson.M{
				"value":     message.value,
				"createdOn": now,
				"updatedOn": now,
			}},
			options.Update().SetUpsert(true),
		)
		if err != nil {
			return err
		}
	}

	return nil
}
