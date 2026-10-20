package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// userAddSignUpProvider marks every existing account as an email/password
// (Houseflow) sign-up. Documents that already carry the field are left alone.
type userAddSignUpProvider struct{}

func (m *userAddSignUpProvider) Version() string { return "0048" }
func (m *userAddSignUpProvider) Name() string    { return "userAddSignUpProvider" }

func (m *userAddSignUpProvider) Up(ctx context.Context, db *mongo.Database) error {
	_, err := db.Collection("User").UpdateMany(ctx,
		bson.M{"signUpProvider": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"signUpProvider": 0}},
	)
	return err
}
