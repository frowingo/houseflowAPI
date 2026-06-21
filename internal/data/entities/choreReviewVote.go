package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ChoreReviewVote struct {
	Id          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ChoreId     string             `bson:"choreId" json:"choreId"`
	HouseId     string             `bson:"houseId" json:"houseId"`
	ReviewRound int                `bson:"reviewRound" json:"reviewRound"`
	ReviewerId  string             `bson:"reviewerId" json:"reviewerId"`
	IsApproved  bool               `bson:"isApproved" json:"isApproved"`
	CreatedOn   time.Time          `bson:"createdOn" json:"createdOn"`
}
