package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// UserIdentity links a third-party provider account to a user. A provider
// account (provider + providerUserId) belongs to at most one user.
type UserIdentity struct {
	Id             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserId         string             `bson:"userId" json:"userId"`
	Provider       AuthProvider       `bson:"provider" json:"provider"`
	ProviderUserId string             `bson:"providerUserId" json:"providerUserId"`
	ProviderEmail  string             `bson:"providerEmail" json:"providerEmail"`
	EmailVerified  bool               `bson:"emailVerified" json:"emailVerified"`
	IsPrivateRelay bool               `bson:"isPrivateRelay" json:"isPrivateRelay"`
	// ProviderClientId is our client id the provider issued the token for
	// (an iOS bundle id, an Apple Services ID, a Google client id).
	ProviderClientId string `bson:"providerClientId" json:"-"`
	// AppleRefreshTokenEnc is the encrypted Apple refresh token kept only to
	// revoke the authorization when the account is deleted.
	AppleRefreshTokenEnc string    `bson:"appleRefreshTokenEnc,omitempty" json:"-"`
	CreatedOn            time.Time `bson:"createdOn" json:"createdOn"`
	LastLoginOn          time.Time `bson:"lastLoginOn" json:"lastLoginOn"`
}
