package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const retiredFlappyBirdGameKey = "flappyBird"

// Flappy Bird was a protocol v1 catalog entry without a runtime: a running
// session could never finish and kept its house's active slot forever.
type retireFlappyBird struct{}

func (*retireFlappyBird) Version() string { return "0043" }
func (*retireFlappyBird) Name() string    { return "retireFlappyBird" }
func (*retireFlappyBird) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	_, err := db.Collection("GameSession").UpdateMany(ctx, bson.M{
		"gameKey": retiredFlappyBirdGameKey,
		"state":   bson.M{"$nin": bson.A{"finished", "cancelled"}},
	}, bson.M{
		"$set":   bson.M{"state": "cancelled", "endReason": "gameRetired", "endedAt": now, "updatedAt": now},
		"$unset": bson.M{"readyWindowEndsAt": "", "countdownEndsAt": ""},
		"$inc":   bson.M{"version": int64(1)},
	})
	if err != nil {
		return err
	}
	_, err = db.Collection(activeGameSessionCollection).DeleteMany(ctx, bson.M{"gameKey": retiredFlappyBirdGameKey})
	return err
}
