package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type httpErrorContractLocalizations struct{}

func (m *httpErrorContractLocalizations) Version() string { return "0032" }
func (m *httpErrorContractLocalizations) Name() string    { return "httpErrorContractLocalizations" }

func (m *httpErrorContractLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{language: "en", key: "common.error.internal_server_error", value: "An unexpected error occurred."},
		{language: "tr", key: "common.error.internal_server_error", value: "Beklenmeyen bir hata oluştu."},
		{language: "en", key: "database.error.transaction_unavailable", value: "The operation is temporarily unavailable. Please try again."},
		{language: "tr", key: "database.error.transaction_unavailable", value: "İşlem geçici olarak kullanılamıyor. Lütfen tekrar deneyin."},
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
