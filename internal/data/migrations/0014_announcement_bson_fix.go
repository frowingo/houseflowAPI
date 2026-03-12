package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// announcementBsonFix renames legacy "ID" field to "_id" for any documents
// written before bson tags were added to the Announcement entity.
// On a fresh deployment this migration is a no-op (no documents to update).
type announcementBsonFix struct{}

func (m *announcementBsonFix) Version() string { return "0014" }
func (m *announcementBsonFix) Name() string    { return "announcement_bson_fix" }

func (m *announcementBsonFix) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("Announcement")

	// Rename "ID" → "_id" for documents that have a string "ID" field instead of "_id".
	_, err := col.UpdateMany(
		ctx,
		bson.M{"ID": bson.M{"$exists": true}},
		[]bson.M{
			{"$set": bson.M{"_id": "$ID"}},
			{"$unset": "ID"},
		},
	)
	return err
}
