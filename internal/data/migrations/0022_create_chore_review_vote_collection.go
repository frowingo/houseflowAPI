package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createChoreReviewVoteCollection struct{}

func (m *createChoreReviewVoteCollection) Version() string { return "0022" }
func (m *createChoreReviewVoteCollection) Name() string {
	return "createChoreReviewVoteCollection"
}

func (m *createChoreReviewVoteCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"choreId", "houseId", "reviewRound", "reviewerId", "isApproved", "createdOn"},
			"properties": bson.M{
				"choreId":     bson.M{"bsonType": "string"},
				"houseId":     bson.M{"bsonType": "string"},
				"reviewRound": bson.M{"bsonType": "int"},
				"reviewerId":  bson.M{"bsonType": "string"},
				"isApproved":  bson.M{"bsonType": "bool"},
				"createdOn":   bson.M{"bsonType": "date"},
			},
		},
	}

	err := db.CreateCollection(ctx, "ChoreReviewVote", options.CreateCollection().SetValidator(validator))
	if err != nil && !isCollectionExistsError(err) {
		return err
	}

	col := db.Collection("ChoreReviewVote")
	_, err = col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "choreId", Value: 1},
				{Key: "reviewRound", Value: 1},
			},
			Options: options.Index().SetName("idxChoreReviewVoteChoreRound"),
		},
		{
			Keys: bson.D{
				{Key: "choreId", Value: 1},
				{Key: "reviewRound", Value: 1},
				{Key: "reviewerId", Value: 1},
			},
			Options: options.Index().SetName("uqChoreReviewVoteReviewerRound").SetUnique(true),
		},
	})
	return err
}
