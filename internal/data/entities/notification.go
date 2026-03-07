package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Notification struct {
	Id           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title        string             `bson:"title" json:"title"`
	Message      string             `bson:"message" json:"message"`
	CreatedAt    time.Time          `bson:"created_at" json:"createdAt"`
	HouseId      string             `bson:"house_id" json:"houseId"`
	HouseOwnerId string             `bson:"house_owner_id" json:"houseOwnerId"`
	IsRead       bool               `bson:"is_read" json:"isRead"`
}
