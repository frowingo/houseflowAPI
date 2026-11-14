package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// queryIndexes backs queries that previously scanned or blocking-sorted:
// the chore deadline scan, house details status histories and the houseId
// deletes of account removal. Existing indexes stay for rolling deploys.
type queryIndexes struct{}

func (*queryIndexes) Version() string { return "0064" }
func (*queryIndexes) Name() string    { return "queryIndexes" }

func (*queryIndexes) Up(ctx context.Context, db *mongo.Database) error {
	for _, target := range []struct {
		collection, name string
		keys             bson.D
	}{
		{"Chore", "idxChoreStatusDueDate", bson.D{{Key: "status", Value: 1}, {Key: "dueDate", Value: 1}, {Key: "_id", Value: 1}}},
		{"ChoreStatusHistory", "idxChoreStatusHistoryChoreIdDateTime", bson.D{{Key: "choreId", Value: 1}, {Key: "dateTime", Value: 1}, {Key: "_id", Value: 1}}},
		{"ChoreReviewVote", "idxChoreReviewVoteHouseId", bson.D{{Key: "houseId", Value: 1}}},
		{"GameMatchResult", "idxGameMatchResultHouseId", bson.D{{Key: "houseId", Value: 1}}},
	} {
		if _, err := db.Collection(target.collection).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys:    target.keys,
			Options: options.Index().SetName(target.name),
		}); err != nil {
			return err
		}
	}
	return nil
}
