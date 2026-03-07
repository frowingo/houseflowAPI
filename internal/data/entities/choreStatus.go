package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ChoreStatusHistory struct {
	Id       primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ChoreId  string             `bson:"choreId" json:"choreId"`
	Status   ChoreStatus        `bson:"status" json:"status"`
	DateTime time.Time          `bson:"dateTime" json:"dateTime"`
	Updater  string             `bson:"updater" json:"updater"`
}
