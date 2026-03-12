package migration

import (
	"context"
	"fmt"
	"log"

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

// RunAll applies every migration in the provided slice that has not yet been
// recorded in the _migrations tracking collection.
// Migrations are executed in slice order. If any migration fails the function
// returns immediately with an error and the remaining migrations are skipped.
func RunAll(ctx context.Context, db *mongo.Database, migrations []Migration) error {
	tracker := NewTracker(db)

	if err := tracker.EnsureIndex(ctx); err != nil {
		return fmt.Errorf("migration tracker index: %w", err)
	}

	for _, m := range migrations {
		applied, err := tracker.IsApplied(ctx, m.Version())
		if err != nil {
			return fmt.Errorf("checking migration %s: %w", m.Version(), err)
		}
		if applied {
			log.Printf("[migration] skip  %s_%s (already applied)", m.Version(), m.Name())
			continue
		}

		log.Printf("[migration] apply %s_%s ...", m.Version(), m.Name())
		if err := m.Up(ctx, db); err != nil {
			return fmt.Errorf("migration %s_%s failed: %w", m.Version(), m.Name(), err)
		}

		if err := tracker.MarkApplied(ctx, m.Version(), m.Name()); err != nil {
			return fmt.Errorf("recording migration %s_%s: %w", m.Version(), m.Name(), err)
		}
		log.Printf("[migration] done  %s_%s", m.Version(), m.Name())
	}

	return nil
}
