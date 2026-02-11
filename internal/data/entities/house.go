package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type House struct {
	Id             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	OwnerId        string             `bson:"ownerId" json:"ownerId"`
	InviteCode     string             `bson:"inviteCode" json:"inviteCode"`
	Name           string             `bson:"name" json:"name"`
	Type           HouseType          `bson:"type" json:"type"`
	MemberIds      []string           `bson:"memberIds" json:"memberIds"`
	MaxMemberCount int                `bson:"maxMemberCount" json:"maxMemberCount"`
	CreatedOn      time.Time          `bson:"createdOn" json:"createdOn"`
	UpdatedOn      time.Time          `bson:"updatedOn" json:"updatedOn"`
}

type HouseType string

const (
	StudentHouse HouseType = "StudentHouse"
	SharedHouse  HouseType = "SharedHouse"
	DormRoom     HouseType = "DormRoom"
)
