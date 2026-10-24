package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// houseTanksLocalizations adds the result and lobby messages of House Tanks.
// Its Game document is seeded by 0054.
type houseTanksLocalizations struct{}

func (m *houseTanksLocalizations) Version() string { return "0053" }
func (m *houseTanksLocalizations) Name() string    { return "houseTanksLocalizations" }

func (m *houseTanksLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "houseTanks.error.result_not_found", "The match result has not been committed yet."},
		{"tr", "houseTanks.error.result_not_found", "Maç sonucu henüz kaydedilmedi."},
		{"en", "houseTanks.error.result_conflict", "This match already has a different committed result."},
		{"tr", "houseTanks.error.result_conflict", "Bu maçın kayıtlı sonucu değiştirilemez."},
		{"en", "houseTanks.error.invalid_result", "The match result is inconsistent."},
		{"tr", "houseTanks.error.invalid_result", "Maç sonucu tutarlı değil."},
		{"en", "houseTanks.error.finalizing", "The match result is being saved."},
		{"tr", "houseTanks.error.finalizing", "Maç sonucu kaydediliyor."},
		{"en", "houseTanks.error.runtime_completion_required", "Running matches must be completed by the room owner."},
		{"tr", "houseTanks.error.runtime_completion_required", "Başlamış maçlar oyun odasının yöneticisi tarafından tamamlanmalıdır."},
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
