package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type gameMatchResult struct{}

func (*gameMatchResult) Version() string { return "0042" }
func (*gameMatchResult) Name() string    { return "gameMatchResult" }
func (*gameMatchResult) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{"$jsonSchema": bson.M{"bsonType": "object", "required": bson.A{"_id", "houseId", "gameKey", "schemaVersion", "status", "endReason", "startedAt", "endedAt", "resultEventType", "runtimeEpoch", "payload", "payloadHash", "createdAt"}, "properties": bson.M{
		"_id":           bson.M{"bsonType": "string", "minLength": 1},
		"houseId":       bson.M{"bsonType": "string", "minLength": 1},
		"gameKey":       bson.M{"bsonType": "string", "minLength": 1},
		"schemaVersion": bson.M{"bsonType": "int", "minimum": 1},
		"status":        bson.M{"enum": bson.A{"completed", "cancelled"}},
		"endReason":     bson.M{"bsonType": "string", "minLength": 1},
		"startedAt":     bson.M{"bsonType": "date"}, "endedAt": bson.M{"bsonType": "date"},
		"resultEventType": bson.M{"bsonType": "string", "minLength": 1},
		"runtimeEpoch":    bson.M{"bsonType": "long", "minimum": 1},
		"payload":         bson.M{"bsonType": "binData"},
		"payloadHash":     bson.M{"bsonType": "string", "minLength": 64, "maxLength": 64},
		"createdAt":       bson.M{"bsonType": "date"},
	}}}
	err := db.CreateCollection(ctx, "GameMatchResult", options.CreateCollection().SetValidator(validator))
	if isCollectionExistsError(err) {
		err = db.RunCommand(ctx, bson.D{{Key: "collMod", Value: "GameMatchResult"}, {Key: "validator", Value: validator}, {Key: "validationLevel", Value: "strict"}, {Key: "validationAction", Value: "error"}}).Err()
	}
	if err != nil {
		return err
	}
	messages := []struct{ key, english, turkish string }{
		{"houseRockets.error.result_not_found", "The match result has not been committed yet.", "Maç sonucu henüz kaydedilmedi."},
		{"houseRockets.error.result_conflict", "This match already has a different committed result.", "Bu maçın kayıtlı sonucu değiştirilemez."},
		{"houseRockets.error.invalid_result", "The match result is inconsistent.", "Maç sonucu tutarlı değil."},
		{"houseRockets.error.finalizing", "The match result is being saved.", "Maç sonucu kaydediliyor."},
		{"houseRockets.error.runtime_completion_required", "Running matches must be completed by the room owner.", "Başlamış maçlar oyun odasının yöneticisi tarafından tamamlanmalıdır."},
	}
	for _, message := range messages {
		for language, value := range map[string]string{"en": message.english, "tr": message.turkish} {
			_, err := db.Collection("localization").UpdateOne(ctx, bson.M{"language": language, "type": "message", "key": message.key}, bson.M{"$setOnInsert": bson.M{"value": value, "createdOn": time.Now().UTC(), "updatedOn": time.Now().UTC()}}, options.Update().SetUpsert(true))
			if err != nil {
				return err
			}
		}
	}
	return nil
}
