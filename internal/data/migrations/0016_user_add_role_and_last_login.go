package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type userAddRoleAndLastLogin struct{}

func (m *userAddRoleAndLastLogin) Version() string { return "0016" }
func (m *userAddRoleAndLastLogin) Name() string    { return "userAddRoleAndLastLogin" }

func (m *userAddRoleAndLastLogin) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("User")

	// role alanı olmayan tüm dokümanlara varsayılan değer (0 = Normal) ata
	_, err := col.UpdateMany(
		ctx,
		bson.M{"role": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"role": 0}},
	)
	if err != nil {
		return err
	}

	// lastLogin sıfır değerinde olan dokümanlara createdOn değerini ata
	zeroTime := primitive.NewDateTimeFromTime(time.Time{})
	_, err = col.UpdateMany(
		ctx,
		bson.M{"$or": bson.A{
			bson.M{"lastLogin": bson.M{"$exists": false}},
			bson.M{"lastLogin": zeroTime},
		}},
		mongo.Pipeline{
			{{"$set", bson.M{"lastLogin": "$createdOn"}}},
		},
	)
	return err
}
