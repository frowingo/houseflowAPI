package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Announcement struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title        string             `bson:"title" json:"title"`
	Description  string             `bson:"description" json:"description"`
	UserId       string             `bson:"userId" json:"userId"`
	HouseId      string             `bson:"houseId" json:"houseId"`
	CreatedOn    time.Time          `bson:"createdOn" json:"createdOn"`
	DisplayUntil time.Time          `bson:"displayUntil" json:"displayUntil"`
}
