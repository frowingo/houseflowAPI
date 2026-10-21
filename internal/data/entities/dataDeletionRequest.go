package entities

import "time"

const DataDeletionCompleted = "completed"

// DataDeletionRequest records a provider's data deletion request so the user
// can look up its status with the confirmation code. It holds no personal
// data: the provider user id is not stored.
type DataDeletionRequest struct {
	Id             string       `bson:"_id" json:"confirmationCode"`
	Provider       AuthProvider `bson:"provider" json:"provider"`
	Status         string       `bson:"status" json:"status"`
	AccountDeleted bool         `bson:"accountDeleted" json:"-"`
	RequestedOn    time.Time    `bson:"requestedOn" json:"requestedOn"`
	ExpiresAt      time.Time    `bson:"expiresAt" json:"-"`
}
