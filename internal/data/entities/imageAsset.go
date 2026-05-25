package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ImageAsset struct {
	Id        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	FileName  string             `bson:"fileName" json:"fileName"`
	FileURL   string             `bson:"fileUrl" json:"fileUrl"`
	PublicID  string             `bson:"publicId" json:"publicId"`
	CreatedOn time.Time          `bson:"createdOn" json:"createdOn"`
	UpdatedOn time.Time          `bson:"updatedOn" json:"updatedOn"`
}
