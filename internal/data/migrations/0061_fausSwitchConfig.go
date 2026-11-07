package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// fausSwitchConfig enables Faus Switch for every house (decision K3). Rules
// and settings are left out so sessions start with the code defaults; an
// operator narrows the rollout or tunes values through the game config API.
// An existing document is never overwritten.
type fausSwitchConfig struct{}

func (m *fausSwitchConfig) Version() string { return "0061" }
func (m *fausSwitchConfig) Name() string    { return "fausSwitchConfig" }

func (m *fausSwitchConfig) Up(ctx context.Context, db *mongo.Database) error {
	_, err := db.Collection(gameConfigCollection).UpdateOne(ctx, bson.M{"_id": "fausSwitch"}, bson.M{"$setOnInsert": bson.M{
		"enabled":   true,
		"rollout":   bson.M{"houseIds": bson.A{}},
		"version":   int64(1),
		"updatedAt": time.Now().UTC(),
		"updatedBy": "migration:0061",
	}}, options.Update().SetUpsert(true))
	return err
}
