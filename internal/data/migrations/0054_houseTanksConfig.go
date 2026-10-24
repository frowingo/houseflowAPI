package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// houseTanksConfig enables House Tanks for every house. Rules and settings
// are left out so sessions start with the code defaults; an operator narrows
// the rollout or tunes values through the game config API. An existing
// document is never overwritten.
type houseTanksConfig struct{}

func (m *houseTanksConfig) Version() string { return "0054" }
func (m *houseTanksConfig) Name() string    { return "houseTanksConfig" }

func (m *houseTanksConfig) Up(ctx context.Context, db *mongo.Database) error {
	_, err := db.Collection(gameConfigCollection).UpdateOne(ctx, bson.M{"_id": "houseTanks"}, bson.M{"$setOnInsert": bson.M{
		"enabled":   true,
		"rollout":   bson.M{"houseIds": bson.A{}},
		"version":   int64(1),
		"updatedAt": time.Now().UTC(),
		"updatedBy": "migration:0054",
	}}, options.Update().SetUpsert(true))
	return err
}
