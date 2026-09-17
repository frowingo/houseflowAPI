package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const migrationsCollection = "_migrations"

type migrationRecord struct {
	Version   string    `bson:"version"`
	Name      string    `bson:"name"`
	AppliedAt time.Time `bson:"appliedAt"`
}

type tracker struct {
	collection *mongo.Collection
}

func newTracker(db *mongo.Database) *tracker {
	return &tracker{collection: db.Collection(migrationsCollection)}
}

func (t *tracker) ensureIndex(ctx context.Context) error {
	_, err := t.collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "version", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	return err
}

func (t *tracker) isApplied(ctx context.Context, version string) (bool, error) {
	count, err := t.collection.CountDocuments(ctx, bson.M{"version": version})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (t *tracker) markApplied(ctx context.Context, version, name string) error {
	_, err := t.collection.InsertOne(ctx, migrationRecord{
		Version:   version,
		Name:      name,
		AppliedAt: time.Now().UTC(),
	})
	return err
}
