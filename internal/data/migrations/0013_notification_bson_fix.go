package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// notificationBsonFix renames legacy json-tagged field names to the canonical
// bson field names defined in the updated Notification entity.
// On a fresh deployment this migration is a no-op (no documents to update).
type notificationBsonFix struct{}

func (m *notificationBsonFix) Version() string { return "0013" }
func (m *notificationBsonFix) Name() string    { return "notification_bson_fix" }

func (m *notificationBsonFix) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("Notification")

	// Rename "Id" → "_id" for documents that have a string "Id" field instead of "_id".
	// All other fields already match their json tag names which also match the new bson tags.
	_, err := col.UpdateMany(
		ctx,
		bson.M{"Id": bson.M{"$exists": true}},
		[]bson.M{
			{"$set": bson.M{"_id": "$Id"}},
			{"$unset": "Id"},
		},
	)
	return err
}
