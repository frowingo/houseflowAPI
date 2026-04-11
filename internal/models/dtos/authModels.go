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
	Issuer     string    `bson:"issuer" json:"issuer"`
	Subject    string    `bson:"subject" json:"subject"`
	IssuerRole int       `bson:"issuerRole" json:"issuerRole"`
	ExpiresAt  time.Time `bson:"expiresAt" json:"expiresAt"`
	IssuedAt   time.Time `bson:"issuedAt" json:"issuedAt"`
}
