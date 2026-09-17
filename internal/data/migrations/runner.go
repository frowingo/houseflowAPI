package migrations

import (
	"context"
	"fmt"
	"log"

	"go.mongodb.org/mongo-driver/mongo"
)

// RunAll applies every migration that has not yet been recorded in the
// _migrations tracking collection. Migrations run in the supplied order and
// execution stops on the first failure.
func RunAll(ctx context.Context, db *mongo.Database, migrations []Migration) error {
	tracker := newTracker(db)

	if err := tracker.ensureIndex(ctx); err != nil {
		return fmt.Errorf("migration tracker index: %w", err)
	}

	for _, migration := range migrations {
		applied, err := tracker.isApplied(ctx, migration.Version())
		if err != nil {
			return fmt.Errorf("checking migration %s: %w", migration.Version(), err)
		}
		if applied {
			log.Printf("[migration] skip  %s_%s (already applied)", migration.Version(), migration.Name())
			continue
		}

		log.Printf("[migration] apply %s_%s ...", migration.Version(), migration.Name())
		if err := migration.Up(ctx, db); err != nil {
			return fmt.Errorf("migration %s_%s failed: %w", migration.Version(), migration.Name(), err)
		}

		if err := tracker.markApplied(ctx, migration.Version(), migration.Name()); err != nil {
			return fmt.Errorf("recording migration %s_%s: %w", migration.Version(), migration.Name(), err)
		}
		log.Printf("[migration] done  %s_%s", migration.Version(), migration.Name())
	}

	return nil
}
