package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type userAgeTobirthDay struct{}

func (m *userAgeTobirthDay) Version() string { return "0021" }
func (m *userAgeTobirthDay) Name() string    { return "userAgeToBirthDay" }

func (m *userAgeTobirthDay) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("User")

	zeroTime := primitive.NewDateTimeFromTime(time.Time{})

	_, err := col.UpdateMany(
		ctx,
		bson.M{"$or": bson.A{
			bson.M{"birthDay": bson.M{"$exists": false}},
			bson.M{"birthDay": bson.M{"$type": "object"}},
		}},
		bson.M{"$set": bson.M{"birthDay": zeroTime}},
	)
	if err != nil {
		return err
	}

	// age alanını tüm dokümanlardan kaldır
	_, err = col.UpdateMany(
		ctx,
		bson.M{"age": bson.M{"$exists": true}},
		bson.M{"$unset": bson.M{"age": ""}},
	)
	return err
}
