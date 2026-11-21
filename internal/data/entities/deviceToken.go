package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type DevicePlatform string

const (
	IOSDevice     DevicePlatform = "ios"
	AndroidDevice DevicePlatform = "android"
)

// DeviceToken is an FCM registration token of a signed-in device. A token
// belongs to one user at a time; signing in with another account moves it.
type DeviceToken struct {
	Id        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserId    string             `bson:"userId" json:"userId"`
	Token     string             `bson:"token" json:"token"`
	Platform  DevicePlatform     `bson:"platform" json:"platform"`
	CreatedOn time.Time          `bson:"createdOn" json:"createdOn"`
	UpdatedOn time.Time          `bson:"updatedOn" json:"updatedOn"`
}
