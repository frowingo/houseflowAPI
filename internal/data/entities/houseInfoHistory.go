package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	HouseInfoColumnName             = "HouseName"
	HouseInfoColumnProfileImage     = "HouseProfileImage"
	HouseInfoColumnMemberCountLimit = "HouseMemberCountLimit"
	HouseInfoColumnType             = "HouseType"
)

type HouseInfoHistory struct {
	Id         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	HouseId    string             `bson:"houseId" json:"houseId"`
	ColumnName string             `bson:"columnName" json:"columnName"`
	Value      any                `bson:"value" json:"value"`
	UpdatedBy  string             `bson:"updatedBy" json:"updatedBy"`
	UpdateOn   time.Time          `bson:"updateOn" json:"updateOn"`
}

func (HouseInfoHistory) CollectionName() string {
	return "HouseInfoHistory"
}
