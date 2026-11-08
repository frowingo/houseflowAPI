package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// houseRocketsLobbyLocalizations adds the messages of the Faus Rockets lobby
// color claim (houseRockets.selectColor, ADR-030): the session command returns
// them as localized errors. Existing texts are never overwritten.
type houseRocketsLobbyLocalizations struct{}

func (m *houseRocketsLobbyLocalizations) Version() string { return "0063" }
func (m *houseRocketsLobbyLocalizations) Name() string    { return "houseRocketsLobbyLocalizations" }

func (m *houseRocketsLobbyLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "houseRockets.error.not_in_lobby", "Colors can only be chosen in the lobby."},
		{"tr", "houseRockets.error.not_in_lobby", "Renk yalnız lobide seçilebilir."},
		{"en", "houseRockets.error.invalid_input", "This action is not valid."},
		{"tr", "houseRockets.error.invalid_input", "Bu işlem geçerli değil."},
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
