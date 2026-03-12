package migration

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const migrationsCollection = "_migrations"

type MigrationRecord struct {
	Version   string    `bson:"version"`
	Name      string    `bson:"name"`
	AppliedAt time.Time `bson:"applied_at"`
}

type Tracker struct {
	col *mongo.Collection
}

func NewTracker(db *mongo.Database) *Tracker {
	return &Tracker{col: db.Collection(migrationsCollection)}
}

func (t *Tracker) EnsureIndex(ctx context.Context) error {
	_, err := t.col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "version", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	return err
}

func (t *Tracker) IsApplied(ctx context.Context, version string) (bool, error) {
	count, err := t.col.CountDocuments(ctx, bson.M{"version": version})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (t *Tracker) MarkApplied(ctx context.Context, version, name string) error {
	_, err := t.col.InsertOne(ctx, MigrationRecord{
		Version:   version,
		Name:      name,
		AppliedAt: time.Now().UTC(),
	})
	return err
}
