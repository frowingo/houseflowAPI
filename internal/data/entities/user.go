package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type UserRole int

const (
	Normal     UserRole = 0
	Admin      UserRole = 1
	SuperAdmin UserRole = 2
)

// AuthProvider names where an account was created or which provider identity
// is linked to it. Values are stored in the database and must not be renumbered.
type AuthProvider int

const (
	ProviderHouseflow AuthProvider = 0
	ProviderGoogle    AuthProvider = 1
	ProviderApple     AuthProvider = 2
	ProviderFacebook  AuthProvider = 3
	ProviderSnapchat  AuthProvider = 4
)

var authProviderNames = map[string]AuthProvider{
	"google":   ProviderGoogle,
	"apple":    ProviderApple,
	"facebook": ProviderFacebook,
	"snapchat": ProviderSnapchat,
}

// ParseSocialProvider maps a route name such as "google" to its provider.
// The Houseflow (email/password) provider is not a social provider.
func ParseSocialProvider(name string) (AuthProvider, bool) {
	provider, ok := authProviderNames[name]
	return provider, ok
}

type User struct {
	Id                  primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Firstname           string             `bson:"firstName" json:"firstName"`
	Lastname            string             `bson:"lastName" json:"lastName"`
	PhoneNumber         string             `bson:"phoneNumber" json:"phoneNumber"`
	Email               string             `bson:"email" json:"email"`
	HashPassword        string             `bson:"password" json:"-"`
	BirthDay            time.Time          `bson:"birthDay" json:"birthDay"`
	ImageURL            string             `bson:"imageUrl" json:"imageUrl"`
	Language            string             `bson:"language" json:"language"`
	HouseIds            []string           `bson:"houseIds" json:"houseIds"`
	IsActive            bool               `bson:"isActive" json:"isActive"`
	IsVerifyPhone       bool               `bson:"isVerifyPhone" json:"isVerifyPhone"`
	IsVerifyEmail       bool               `bson:"isVerifyEmail" json:"isVerifyEmail"`
	FailedLoginAttempts int                `bson:"failedLoginAttempts" json:"failedLoginAttempts"`
	CreatedOn           time.Time          `bson:"createdOn" json:"createdOn"`
	UpdatedOn           time.Time          `bson:"updatedOn" json:"updatedOn"`
	LastLogin           time.Time          `bson:"lastLogin" json:"lastLogin"`
	Role                UserRole           `bson:"role" json:"role"`
	SignUpProvider      AuthProvider       `bson:"signUpProvider" json:"signUpProvider"`
}
