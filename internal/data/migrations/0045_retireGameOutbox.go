package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
)

// retireGameOutbox runs once every instance writes the activeKey slot. The
// idempotent 0044 steps are repeated first so documents written by the previous
// release during its rolling deploy are not left behind. OutboxMessage has had
// no writer since 0044; ActiveGameSession is still written by 0044-era
// instances during this release's rollout and is dropped one release later.
type retireGameOutbox struct{}

func (*retireGameOutbox) Version() string { return "0045" }
func (*retireGameOutbox) Name() string    { return "retireGameOutbox" }
func (*retireGameOutbox) Up(ctx context.Context, db *mongo.Database) error {
	if err := (&gameDataRetention{}).Up(ctx, db); err != nil {
		return err
	}
	return db.Collection("OutboxMessage").Drop(ctx)
}
