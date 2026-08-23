package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	UserInfoColumnFirstName   = "FirstName"
	UserInfoColumnLastName    = "LastName"
	UserInfoColumnPhoneNumber = "PhoneNumber"
	UserInfoColumnBirthDay    = "BirthDay"
	UserInfoColumnRole        = "Role"
)

type UserInfoHistory struct {
	Id         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserId     string             `bson:"userId" json:"userId"`
	ColumnName string             `bson:"columnName" json:"columnName"`
	Value      any                `bson:"value" json:"value"`
	UpdateOn   time.Time          `bson:"updateOn" json:"updateOn"`
}
