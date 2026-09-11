package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ownerReassignmentLocalizations struct{}

func (m *ownerReassignmentLocalizations) Version() string { return "0033" }
func (m *ownerReassignmentLocalizations) Name() string    { return "ownerReassignmentLocalizations" }

func (m *ownerReassignmentLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now()
	messages := []struct {
		language string
		value    string
	}{
		{language: "en", value: "The user cannot be deleted while they are the only member of a house they own."},
		{language: "tr", value: "Kullanıcı, sahibi olduğu bir evin tek üyesiyken silinemez."},
	}

	for _, message := range messages {
		if _, err := db.Collection("localization").UpdateOne(ctx, bson.M{
			"language": message.language,
			"type":     "message",
			"key":      "user.error.cannot_delete_sole_house_owner",
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
