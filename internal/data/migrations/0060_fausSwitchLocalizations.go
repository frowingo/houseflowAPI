package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// fausSwitchLocalizations adds the result, lobby and race messages of Faus
// Switch. Its Game document is seeded by 0061. Version 0059 belongs to the
// notification delivery work, which lands separately; the runner skips by
// version, so the order they arrive in does not matter.
type fausSwitchLocalizations struct{}

func (m *fausSwitchLocalizations) Version() string { return "0060" }
func (m *fausSwitchLocalizations) Name() string    { return "fausSwitchLocalizations" }

func (m *fausSwitchLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "fausSwitch.error.result_not_found", "The race result has not been saved yet."},
		{"tr", "fausSwitch.error.result_not_found", "Yarış sonucu henüz kaydedilmedi."},
		{"en", "fausSwitch.error.result_conflict", "This race already has a different saved result."},
		{"tr", "fausSwitch.error.result_conflict", "Bu yarışın kayıtlı sonucu değiştirilemez."},
		{"en", "fausSwitch.error.invalid_result", "The race result is inconsistent."},
		{"tr", "fausSwitch.error.invalid_result", "Yarış sonucu tutarlı değil."},
		{"en", "fausSwitch.error.finalizing", "The race result is being saved."},
		{"tr", "fausSwitch.error.finalizing", "Yarış sonucu kaydediliyor."},
		{"en", "fausSwitch.error.runtime_completion_required", "Running races must be completed by the room owner."},
		{"tr", "fausSwitch.error.runtime_completion_required", "Başlamış yarışlar oyun odasının yöneticisi tarafından tamamlanmalıdır."},
		{"en", "fausSwitch.error.not_in_lobby", "Colors can only be chosen in the lobby."},
		{"tr", "fausSwitch.error.not_in_lobby", "Renk yalnız lobide seçilebilir."},
		{"en", "fausSwitch.error.invalid_input", "This action is not valid."},
		{"tr", "fausSwitch.error.invalid_input", "Bu işlem geçerli değil."},
		{"en", "fausSwitch.error.runner_finished", "Your robot is already out of the race."},
		{"tr", "fausSwitch.error.runner_finished", "Robotun yarışın dışında."},
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
