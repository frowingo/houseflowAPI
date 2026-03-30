package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Notification struct {
	Id           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title        string             `bson:"title" json:"title"`
	Message      string             `bson:"message" json:"message"`
	CreatedAt    time.Time          `bson:"createdAt" json:"createdAt"`
	HouseId      string             `bson:"houseId" json:"houseId"`
	HouseOwnerId string             `bson:"houseOwnerId" json:"houseOwnerId"`
	IsRead       bool               `bson:"isRead" json:"isRead"`
}
