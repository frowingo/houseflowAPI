package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type gameSessionCommandReceipts struct{}

func (m *gameSessionCommandReceipts) Version() string { return "0038" }
func (m *gameSessionCommandReceipts) Name() string    { return "gameSessionCommandReceipts" }

func (m *gameSessionCommandReceipts) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{
			"_id", "actorId", "commandId", "commandType", "payloadHash", "sessionId", "processedAt",
		},
		"properties": bson.M{
			"_id":         bson.M{"bsonType": "objectId"},
			"actorId":     bson.M{"bsonType": "string"},
			"commandId":   bson.M{"bsonType": "string"},
			"commandType": bson.M{"bsonType": "string"},
			"payloadHash": bson.M{"bsonType": "string"},
			"sessionId":   bson.M{"bsonType": "string"},
			"processedAt": bson.M{"bsonType": "date"},
		},
	}}
	if err := db.CreateCollection(
		ctx,
		"GameSessionCommandReceipt",
		options.CreateCollection().SetValidator(validator),
	); err != nil && !isCollectionExistsError(err) {
		return err
	}

	if _, err := db.Collection("GameSessionCommandReceipt").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "actorId", Value: 1},
				{Key: "commandId", Value: 1},
			},
			Options: options.Index().SetUnique(true).SetName("uqGameSessionCommandActorCommand"),
		},
		{
			Keys: bson.D{
				{Key: "sessionId", Value: 1},
				{Key: "processedAt", Value: -1},
			},
			Options: options.Index().SetName("idxGameSessionCommandSessionProcessedAt"),
		},
	}); err != nil {
		return err
	}

	return insertGameSessionLocalizations(ctx, db)
}

func insertGameSessionLocalizations(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "game.error.session_not_found", "The game session was not found."},
		{"tr", "game.error.session_not_found", "Oyun oturumu bulunamadı."},
		{"en", "game.error.session_already_exists", "The game session already exists."},
		{"tr", "game.error.session_already_exists", "Oyun oturumu zaten mevcut."},
		{"en", "game.error.session_conflict", "The game session changed while processing the request. Please try again."},
		{"tr", "game.error.session_conflict", "İstek işlenirken oyun oturumu değişti. Lütfen tekrar deneyin."},
		{"en", "game.error.command_id_required", "A command ID is required."},
		{"tr", "game.error.command_id_required", "Command ID zorunludur."},
		{"en", "game.error.command_id_reused", "The command ID was already used for a different request."},
		{"tr", "game.error.command_id_reused", "Command ID daha önce farklı bir istek için kullanıldı."},
		{"en", "game.error.invalid_definition", "The game session definition is invalid."},
		{"tr", "game.error.invalid_definition", "Oyun oturumu tanımı geçersiz."},
		{"en", "game.error.session_full", "The game session is full."},
		{"tr", "game.error.session_full", "Oyun oturumu dolu."},
		{"en", "game.error.player_already_joined", "You already joined this game session."},
		{"tr", "game.error.player_already_joined", "Bu oyun oturumuna zaten katıldınız."},
		{"en", "game.error.player_not_found", "You are not a player in this game session."},
		{"tr", "game.error.player_not_found", "Bu oyun oturumunda oyuncu değilsiniz."},
		{"en", "game.error.join_closed", "This game session no longer accepts players."},
		{"tr", "game.error.join_closed", "Bu oyun oturumu artık oyuncu kabul etmiyor."},
		{"en", "game.error.ready_closed", "Ready status can no longer be changed."},
		{"tr", "game.error.ready_closed", "Hazır durumu artık değiştirilemez."},
		{"en", "game.error.invalid_state", "This operation is not allowed in the current game state."},
		{"tr", "game.error.invalid_state", "Bu işleme mevcut oyun durumunda izin verilmiyor."},
		{"en", "game.error.cancel_forbidden", "Only the session creator or house owner can cancel the game session."},
		{"tr", "game.error.cancel_forbidden", "Oyun oturumunu yalnızca oturumu başlatan kullanıcı veya ev sahibi iptal edebilir."},
		{"en", "game.error.max_players_exceeds_house", "The game player limit cannot exceed the house member limit."},
		{"tr", "game.error.max_players_exceeds_house", "Oyuncu sınırı evin üye sınırını aşamaz."},
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
