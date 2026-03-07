package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Announcement struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title        string             `bson:"title" json:"title"`
	Description  string             `bson:"description" json:"description"`
	UserId       string             `bson:"user_id" json:"userId"`
	HouseId      string             `bson:"house_id" json:"houseId"`
	CreatedOn    time.Time          `bson:"created_on" json:"createdOn"`
	DisplayUntil time.Time          `bson:"display_until" json:"displayUntil"`
}
