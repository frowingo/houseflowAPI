package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type LocalizationLanguageOption struct {
	Id         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Code       string             `bson:"code" json:"prefix"`
	Name       string             `bson:"name" json:"name"`
	NativeName string             `bson:"nativeName" json:"nativeName"`
	IsDefault  bool               `bson:"isDefault" json:"isDefault"`
	IsActive   bool               `bson:"isActive" json:"isActive"`
	Image      string             `bson:"image" json:"image"`
	CreatedOn  time.Time          `bson:"createdOn" json:"createdOn"`
	UpdatedOn  time.Time          `bson:"updatedOn" json:"updatedOn"`
}

func (LocalizationLanguageOption) CollectionName() string {
	return "localization_languages"
}
