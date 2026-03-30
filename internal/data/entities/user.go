package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type User struct {
	Id            primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Firstname     string             `bson:"firstName" json:"firstName"`
	Lastname      string             `bson:"lastName" json:"lastName"`
	PhoneNumber   string             `bson:"phoneNumber" json:"phoneNumber"`
	Email         string             `bson:"email" json:"email"`
	HashPassword  string             `bson:"password" json:"password"`
	Age           int                `bson:"age" json:"age"`
	ImageURL      string             `bson:"imageUrl" json:"imageUrl"`
	HouseIds      []string           `bson:"houseIds" json:"houseIds"`
	IsActive      bool               `bson:"isActive" json:"isActive"`
	IsVerifyPhone bool               `bson:"isVerifyPhone" json:"isVerifyPhone"`
	IsVerifyEmail bool               `bson:"isVerifyEmail" json:"isVerifyEmail"`
	CreatedOn     time.Time          `bson:"createdOn" json:"createdOn"`
	UpdatedOn     time.Time          `bson:"updatedOn" json:"updatedOn"`
	LastLogin     time.Time          `bson:"lastLogin" json:"lastLogin"`
}
