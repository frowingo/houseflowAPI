package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
)

// Migration defines a single, forward-only migration step.
type Migration interface {
	// Version returns a zero-padded string like "0001" used for ordering and deduplication.
	Version() string
	// Name returns a short human-readable description.
	Name() string
	// Up applies the migration against the provided database.
	Up(ctx context.Context, db *mongo.Database) error
}
