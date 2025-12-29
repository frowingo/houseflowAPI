package dtos

import "time"

type LoginRequestModel struct {
	Email    string `bson:"email" json:"email"`
	Password string `bson:"password" json:"password"`
}

type LoginResponseModel struct {
	Email    string `bson:"email" json:"email"`
	Password string `bson:"password" json:"password"`
}

type JwtModel struct {
	Issuer    string    `bson:"issuer" json:"issuer"`
	ExpiresAt time.Time `bson:"expiresAt" json:"expiresAt"`
	IssuedAt  time.Time `bson:"issuedAt" json:"issuedAt"`
}
