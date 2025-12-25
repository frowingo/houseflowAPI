package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type User struct {
	Id          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Firstname   string             `bson:"firstName" json:"firstName"`
	Lastname    string             `bson:"lastName" json:"lastName"`
	PhoneNumber string             `bson:"phoneNumber" json:"phoneNumber"`
	Email       string             `bson:"email" json:"email"`
	Password    string             `bson:"password" json:"password"`
	Age         int                `bson:"age" json:"age"`
	ImageURL    string             `bson:"image_url" json:"image_url"`
	HouseIds    []string           `bson:"house_ids" json:"house_ids"`
	IsActive    bool               `bson:"is_active" json:"is_active"`
	CreatedOn   time.Time          `bson:"created_on" json:"created_on"`
	UpdatedOn   time.Time          `bson:"updated_on" json:"updated_on"`
	LastLogin   time.Time          `bson:"last_login" json:"last_login"`
}
