package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ChoreStatusHistory struct {
	Id       primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ChoreId  string             `bson:"chore_id" json:"chore_id"`
	Status   ChoreStatus        `bson:"status" json:"status"`
	DateTime time.Time          `bson:"date_time" json:"date_time"`
	Updater  string             `bson:"updater" json:"updater"`
}
