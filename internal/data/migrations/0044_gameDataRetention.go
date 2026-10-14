package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	cancelledGameRetention      = 10 * time.Minute
	gameRuntimeOwnerRetention   = time.Hour
	gameCommandReceiptRetention = time.Hour
)

// gameDataRetention moves the active slot onto GameSession and bounds how long
// per-match data lives. The legacy ActiveGameSession pointer and OutboxMessage
// collection stay until the previous release is retired from every instance.
type gameDataRetention struct{}

func (*gameDataRetention) Version() string { return "0044" }
func (*gameDataRetention) Name() string    { return "gameDataRetention" }
func (*gameDataRetention) Up(ctx context.Context, db *mongo.Database) error {
	for _, step := range []func(context.Context, *mongo.Database) error{
		backfillGameSessionActiveKey,
		expireCancelledGameData,
		expireTerminalRuntimeOwners,
		expireGameCommandReceipts,
	} {
		if err := step(ctx, db); err != nil {
			return err
		}
	}
	return nil
}

func backfillGameSessionActiveKey(ctx context.Context, db *mongo.Database) error {
	sessions := db.Collection("GameSession")
	_, err := sessions.UpdateMany(ctx,
		bson.M{"state": bson.M{"$in": bson.A{"lobby", "readyWindow", "countdown", "running"}}},
		bson.A{bson.M{"$set": bson.M{"activeKey": bson.M{"$concat": bson.A{"$houseId", ":", "$gameKey"}}}}})
	if err != nil {
		return err
	}
	// Duplicate active sessions fail the unique index instead of being
	// cancelled silently, matching the ActiveGameSession backfill policy.
	_, err = sessions.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "activeKey", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("uqGameSessionActiveKey").
			SetPartialFilterExpression(bson.M{"activeKey": bson.M{"$exists": true}}),
	})
	return err
}

func expireCancelledGameData(ctx context.Context, db *mongo.Database) error {
	expiry := bson.A{bson.M{"$set": bson.M{"expiresAt": bson.M{"$add": bson.A{"$endedAt", cancelledGameRetention.Milliseconds()}}}}}
	for _, target := range []struct {
		collection, index string
		filter            bson.M
	}{
		{"GameSession", "ttlGameSessionExpiresAt", bson.M{"state": "cancelled", "endedAt": bson.M{"$exists": true}}},
		{"GameMatchResult", "ttlGameMatchResultExpiresAt", bson.M{"status": "cancelled"}},
	} {
		collection := db.Collection(target.collection)
		filter := target.filter
		filter["expiresAt"] = bson.M{"$exists": false}
		if _, err := collection.UpdateMany(ctx, filter, expiry); err != nil {
			return err
		}
		if err := createExpiryIndex(ctx, collection, target.index); err != nil {
			return err
		}
	}
	return nil
}

func expireTerminalRuntimeOwners(ctx context.Context, db *mongo.Database) error {
	owners := db.Collection("GameRuntimeOwner")
	cursor, err := owners.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"expiresAt": bson.M{"$exists": false}}}},
		{{Key: "$lookup", Value: bson.M{"from": "GameSession", "localField": "_id", "foreignField": "_id", "as": "session"}}},
		{{Key: "$project", Value: bson.M{"session.state": 1, "session.endedAt": 1}}},
	})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	now := time.Now().UTC()
	for cursor.Next(ctx) {
		var owner struct {
			SessionID string `bson:"_id"`
			Session   []struct {
				State   string     `bson:"state"`
				EndedAt *time.Time `bson:"endedAt"`
			} `bson:"session"`
		}
		if err := cursor.Decode(&owner); err != nil {
			return err
		}
		endedAt := now
		if len(owner.Session) == 1 {
			session := owner.Session[0]
			if session.State != "finished" && session.State != "cancelled" {
				continue
			}
			if session.EndedAt != nil {
				endedAt = *session.EndedAt
			}
		}
		if _, err := owners.UpdateOne(ctx, bson.M{"_id": owner.SessionID}, bson.M{"$set": bson.M{"expiresAt": endedAt.Add(gameRuntimeOwnerRetention)}}); err != nil {
			return err
		}
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	return createExpiryIndex(ctx, owners, "ttlGameRuntimeOwnerExpiresAt")
}

func expireGameCommandReceipts(ctx context.Context, db *mongo.Database) error {
	_, err := db.Collection("GameSessionCommandReceipt").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "processedAt", Value: 1}},
		Options: options.Index().SetName("ttlGameSessionCommandReceiptProcessedAt").SetExpireAfterSeconds(int32(gameCommandReceiptRetention.Seconds())),
	})
	return err
}

func createExpiryIndex(ctx context.Context, collection *mongo.Collection, name string) error {
	_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expiresAt", Value: 1}},
		Options: options.Index().SetName(name).SetExpireAfterSeconds(0),
	})
	return err
}
