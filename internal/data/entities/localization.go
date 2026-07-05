package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type LocalizationLanguage string

const (
	English LocalizationLanguage = "en"
	Turkish LocalizationLanguage = "tr"
)

type LocalizationType string

const (
	Plaintext LocalizationType = "plaintext"
	Message   LocalizationType = "message"
)

type Localization struct {
	Id        primitive.ObjectID   `bson:"_id,omitempty" json:"id"`
	Language  LocalizationLanguage `bson:"language" json:"language"`
	Key       string               `bson:"key" json:"key"`
	Type      LocalizationType     `bson:"type" json:"type"`
	Value     string               `bson:"value" json:"value"`
	CreatedOn time.Time            `bson:"createdOn" json:"createdOn"`
	UpdatedOn time.Time            `bson:"updatedOn" json:"updatedOn"`
}

func (Localization) CollectionName() string {
	return "localization"
}
