package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type houseInfoHistory struct{}

func (m *houseInfoHistory) Version() string { return "0036" }
func (m *houseInfoHistory) Name() string    { return "houseInfoHistory" }

func (m *houseInfoHistory) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{"houseId", "columnName", "value", "updatedBy", "updateOn"},
		"properties": bson.M{
			"houseId":    bson.M{"bsonType": "string"},
			"columnName": bson.M{"bsonType": "string"},
			"value":      bson.M{"bsonType": bson.A{"string", "int", "long"}},
			"updatedBy":  bson.M{"bsonType": "string"},
			"updateOn":   bson.M{"bsonType": "date"},
		},
	}}
	if err := db.CreateCollection(ctx, "HouseInfoHistory", options.CreateCollection().SetValidator(validator)); err != nil && !isCollectionExistsError(err) {
		return err
	}

	if _, err := db.Collection("HouseInfoHistory").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "houseId", Value: 1},
			{Key: "columnName", Value: 1},
			{Key: "updateOn", Value: -1},
		},
		Options: options.Index().SetName("idxHouseInfoHistoryHouseIdColumnUpdateOn"),
	}); err != nil {
		return err
	}

	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "house.error.owner_required", "Only the house owner can perform this operation."},
		{"tr", "house.error.owner_required", "Bu işlemi yalnızca ev sahibi yapabilir."},
		{"en", "house.error.profile_no_fields", "At least one house profile field must be provided."},
		{"tr", "house.error.profile_no_fields", "En az bir ev profili alanı gönderilmelidir."},
		{"en", "house.error.profile_field_update_limit", "%s can only be updated once every 48 hours."},
		{"tr", "house.error.profile_field_update_limit", "%s alanı yalnızca 48 saatte bir güncellenebilir."},
		{"en", "house.error.member_limit_below_current", "The member limit cannot be lower than the current member count (%s)."},
		{"tr", "house.error.member_limit_below_current", "Üye sınırı mevcut üye sayısından (%s) düşük olamaz."},
		{"en", "house.error.target_not_member", "The target user is not a member of this house."},
		{"tr", "house.error.target_not_member", "Hedef kullanıcı bu evin üyesi değil."},
		{"en", "house.error.cannot_remove_member", "Only the house owner can remove another member."},
		{"tr", "house.error.cannot_remove_member", "Başka bir üyeyi yalnızca ev sahibi çıkarabilir."},
		{"en", "house.error.sole_owner_cannot_exit", "The sole owner cannot leave the house."},
		{"tr", "house.error.sole_owner_cannot_exit", "Evdeki tek üye olan ev sahibi evden ayrılamaz."},
		{"en", "house.error.exit_conflict", "House membership changed while processing the request. Please try again."},
		{"tr", "house.error.exit_conflict", "İstek işlenirken ev üyeliği değişti. Lütfen tekrar deneyin."},
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
