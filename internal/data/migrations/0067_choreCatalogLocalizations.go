package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type choreCatalogLocalizations struct{}

func (*choreCatalogLocalizations) Version() string { return "0067" }
func (*choreCatalogLocalizations) Name() string    { return "choreCatalogLocalizations" }

func (*choreCatalogLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		key      string
		language string
		value    string
	}{
		{"choreCatalog.error.not_found", "en", "The chore catalog entry was not found or has been deleted."},
		{"choreCatalog.error.not_found", "tr", "Görev kataloğu kaydı bulunamadı veya silinmiş."},
		{"choreCatalog.error.system_forbidden", "en", "Only authorized users can add or change system catalog entries."},
		{"choreCatalog.error.system_forbidden", "tr", "Sistem kayıtlarını yalnızca yetkili kullanıcılar ekleyebilir veya değiştirebilir."},
		{"choreCatalog.error.delete_requires_id", "en", "The id of the entry to delete is required."},
		{"choreCatalog.error.delete_requires_id", "tr", "Silinecek kaydın kimliği belirtilmelidir."},
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
