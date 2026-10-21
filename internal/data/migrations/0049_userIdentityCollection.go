package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const userIdentityCollection = "UserIdentity"

// userIdentityCollectionMigration creates the collection of third-party
// provider accounts linked to users. The unique provider/providerUserId index
// guarantees one provider account is never attached to two users, and the
// unique userId/provider index that a user links at most one account per
// provider, including under concurrent requests.
type userIdentityCollectionMigration struct{}

func (m *userIdentityCollectionMigration) Version() string { return "0049" }
func (m *userIdentityCollectionMigration) Name() string    { return "userIdentityCollection" }

func (m *userIdentityCollectionMigration) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{"userId", "provider", "providerUserId", "createdOn"},
		"properties": bson.M{
			"userId":               bson.M{"bsonType": "string", "minLength": 1},
			"provider":             bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
			"providerUserId":       bson.M{"bsonType": "string", "minLength": 1},
			"providerEmail":        bson.M{"bsonType": "string"},
			"emailVerified":        bson.M{"bsonType": "bool"},
			"isPrivateRelay":       bson.M{"bsonType": "bool"},
			"providerClientId":     bson.M{"bsonType": "string"},
			"appleRefreshTokenEnc": bson.M{"bsonType": "string"},
			"createdOn":            bson.M{"bsonType": "date"},
			"lastLoginOn":          bson.M{"bsonType": "date"},
		},
	}}
	err := db.CreateCollection(ctx, userIdentityCollection, options.CreateCollection().SetValidator(validator))
	if err != nil && !isCollectionExistsError(err) {
		return err
	}

	_, err = db.Collection(userIdentityCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "provider", Value: 1}, {Key: "providerUserId", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idxUserIdentityProviderUnique"),
		},
		{
			// One account per provider and user; also serves lookups by user.
			Keys:    bson.D{{Key: "userId", Value: 1}, {Key: "provider", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idxUserIdentityUserProviderUnique"),
		},
	})
	return err
}
