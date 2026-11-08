package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// houseTanksLobbyLocalizations adds the messages of the FauseTanks lobby
// color claim (houseTanks.selectColor, ADR-030): the session command returns
// them as localized errors. Existing texts are never overwritten.
type houseTanksLobbyLocalizations struct{}

func (m *houseTanksLobbyLocalizations) Version() string { return "0062" }
func (m *houseTanksLobbyLocalizations) Name() string    { return "houseTanksLobbyLocalizations" }

func (m *houseTanksLobbyLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "houseTanks.error.not_in_lobby", "Colors can only be chosen in the lobby."},
		{"tr", "houseTanks.error.not_in_lobby", "Renk yalnız lobide seçilebilir."},
		{"en", "houseTanks.error.invalid_input", "This action is not valid."},
		{"tr", "houseTanks.error.invalid_input", "Bu işlem geçerli değil."},
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
