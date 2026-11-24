package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// choreArchive prepares the chore archive. Closed chores of both outcomes now
// order by completedAt, so failed chores, which used to clear it, get the
// time they closed and completedBy "system". The daily deadline pass dates
// any an older instance still writes undated during the rollout.
type choreArchive struct{}

// failedChoreStatus is entities.Failed; migrations keep their own copy.
const failedChoreStatus = 4

func (m *choreArchive) Version() string { return "0071" }
func (m *choreArchive) Name() string    { return "choreArchive" }

func (m *choreArchive) Up(ctx context.Context, db *mongo.Database) error {
	chores := db.Collection("Chore")
	// Serves the archive page and the recently closed chores of house
	// details: houseId equality, completedAt range, newest first.
	if _, err := chores.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "houseId", Value: 1}, {Key: "completedAt", Value: -1}, {Key: "_id", Value: -1}},
		Options: options.Index().SetName("idxChoreHouseCompletedAt"),
	}); err != nil {
		return err
	}

	if err := dateFailedChores(ctx, chores, db.Collection("ChoreStatusHistory")); err != nil {
		return err
	}

	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "chore.error.archive_invalid_range", "Send from and to as RFC 3339 dates, with from before to."},
		{"tr", "chore.error.archive_invalid_range", "from ve to RFC 3339 tarihleri olmalı ve from, to'dan önce olmalı."},
		{"en", "chore.error.archive_invalid_cursor", "The archive cursor is not valid."},
		{"tr", "chore.error.archive_invalid_cursor", "Arşiv imleci geçersiz."},
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

// dateFailedChores mirrors the deadline service's FailedChoreCloseTime: the
// last failed history, but no later than the due date, where a deadline
// failure closes.
func dateFailedChores(ctx context.Context, chores, histories *mongo.Collection) error {
	undated := bson.M{"status": failedChoreStatus, "completedAt": bson.M{"$in": bson.A{nil, time.Time{}}}}
	cursor, err := chores.Find(ctx, undated, options.Find().SetProjection(bson.M{"_id": 1, "dueDate": 1}))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var chore struct {
			Id      primitive.ObjectID `bson:"_id"`
			DueDate time.Time          `bson:"dueDate"`
		}
		if err := cursor.Decode(&chore); err != nil {
			return err
		}
		var history struct {
			DateTime time.Time `bson:"dateTime"`
		}
		err := histories.FindOne(ctx, bson.M{"choreId": chore.Id.Hex(), "status": failedChoreStatus},
			options.FindOne().SetSort(bson.D{{Key: "dateTime", Value: -1}, {Key: "_id", Value: -1}}),
		).Decode(&history)
		if err != nil && err != mongo.ErrNoDocuments {
			return err
		}

		closedAt := chore.Id.Timestamp()
		switch {
		case !history.DateTime.IsZero() && (chore.DueDate.IsZero() || history.DateTime.Before(chore.DueDate)):
			closedAt = history.DateTime
		case !chore.DueDate.IsZero():
			closedAt = chore.DueDate
		}

		filter := bson.M{"_id": chore.Id, "status": failedChoreStatus, "completedAt": bson.M{"$in": bson.A{nil, time.Time{}}}}
		if _, err := chores.UpdateOne(ctx, filter, bson.M{
			"$set": bson.M{"completedAt": closedAt, "completedBy": "system"},
			"$inc": bson.M{"version": 1},
		}); err != nil {
			return err
		}
	}
	return cursor.Err()
}
