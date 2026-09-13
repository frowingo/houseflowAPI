package migrations

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type houseInviteCodes struct{}

func (m *houseInviteCodes) Version() string { return "0035" }
func (m *houseInviteCodes) Name() string    { return "houseInviteCodes" }

func (m *houseInviteCodes) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{"_id", "codeDigest", "expiresAt", "generatedBy", "generatedOn"},
		"properties": bson.M{
			"_id":         bson.M{"bsonType": "objectId"},
			"codeDigest":  bson.M{"bsonType": "string"},
			"expiresAt":   bson.M{"bsonType": "date"},
			"generatedBy": bson.M{"bsonType": "string"},
			"generatedOn": bson.M{"bsonType": "date"},
		},
	}}
	if err := db.CreateCollection(ctx, "HouseInviteCode", options.CreateCollection().SetValidator(validator)); err != nil && !isCollectionExistsError(err) {
		return err
	}

	inviteCollection := db.Collection("HouseInviteCode")
	if _, err := inviteCollection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "codeDigest", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uqHouseInviteCodeDigest"),
		},
		{
			Keys:    bson.D{{Key: "expiresAt", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(0).SetName("ttlHouseInviteCodeExpiresAt"),
		},
	}); err != nil {
		return err
	}

	houseCollection := db.Collection("House")
	if _, err := houseCollection.Indexes().DropOne(ctx, "idxHouseInviteCodeUnique"); err != nil && !isIndexNotFoundError(err) {
		return err
	}
	if _, err := houseCollection.UpdateMany(ctx, bson.M{}, bson.M{"$unset": bson.M{"inviteCode": ""}}); err != nil {
		return err
	}

	now := time.Now()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{language: "en", key: "house.error.invalid_or_expired_invite_code", value: "The invite code is invalid or has expired."},
		{language: "tr", key: "house.error.invalid_or_expired_invite_code", value: "Davet kodu geçersiz veya süresi dolmuş."},
		{language: "en", key: "house.error.only_owner_can_generate_invite_code", value: "Only the house owner can generate an invite code."},
		{language: "tr", key: "house.error.only_owner_can_generate_invite_code", value: "Davet kodunu yalnızca ev sahibi oluşturabilir."},
		{language: "en", key: "house.error.failed_generate_invite_code", value: "The invite code could not be generated."},
		{language: "tr", key: "house.error.failed_generate_invite_code", value: "Davet kodu oluşturulamadı."},
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

func isIndexNotFoundError(err error) bool {
	var commandError mongo.CommandError
	return errors.As(err, &commandError) && commandError.Code == 27
}
