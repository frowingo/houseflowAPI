package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// turnGameLocalizations adds the rejection messages every turn-based game
// shares. A turn game ships without a Game document and so stays switched off
// until an operator enables it; nothing is seeded for it here.
type turnGameLocalizations struct{}

func (m *turnGameLocalizations) Version() string { return "0047" }
func (m *turnGameLocalizations) Name() string    { return "turnGameLocalizations" }

func (m *turnGameLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "game.error.not_your_turn", "It is another player's turn."},
		{"tr", "game.error.not_your_turn", "Sıra başka bir oyuncuda."},
		{"en", "game.error.invalid_move", "This move is not allowed."},
		{"tr", "game.error.invalid_move", "Bu hamleye izin verilmiyor."},
	}
	for _, message := range messages {
		if _, err := db.Collection("localization").UpdateOne(ctx, bson.M{
			"language": message.language,
			"type":     "message",
			"key":      message.key,
		}, bson.M{"$setOnInsert": bson.M{
			"value":     message.value,
			"createdOn": now,
			"updatedOn": now,
		}}, options.Update().SetUpsert(true)); err != nil {
			return err
		}
	}
	return nil
}
