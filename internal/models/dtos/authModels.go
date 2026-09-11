package dtos

type LoginRequestModel struct {
	Email    string `bson:"email" json:"email"`
	Password string `bson:"password" json:"password"`
}

type LoginResponseModel struct {
	Email    string `bson:"email" json:"email"`
	Password string `bson:"password" json:"password"`
}

type IsAuthResponseModel struct {
	Success bool                 `json:"success"`
	Data    *AuthUserResultModel `json:"data,omitempty"`
}

type AuthHouseModel struct {
	HouseID      string `json:"houseId"`
	HouseName    string `json:"houseName"`
	HouseProfile string `json:"houseProfile"`
}

type AuthUserResultModel struct {
	Id            string           `json:"id"`
	Firstname     string           `json:"firstName"`
	Lastname      string           `json:"lastName"`
	PhoneNumber   string           `json:"phoneNumber"`
	Email         string           `json:"email"`
	BirthDay      UTCDateTime      `json:"birthDay"`
	ImageURL      string           `json:"imageUrl"`
	Language      string           `json:"language"`
	HouseList     []AuthHouseModel `json:"houseList"`
	IsActive      bool             `json:"isActive"`
	IsVerifyPhone bool             `json:"isVerifyPhone"`
	IsVerifyEmail bool             `json:"isVerifyEmail"`
	CreatedOn     UTCDateTime      `json:"createdOn"`
	UpdatedOn     UTCDateTime      `json:"updatedOn"`
	LastLogin     UTCDateTime      `json:"lastLogin"`
}

type SuccessResponseModel struct {
	Success bool `json:"success"`
}

type JwtModel struct {
	Issuer     string      `bson:"issuer" json:"issuer"`
	Subject    string      `bson:"subject" json:"subject"`
	IssuerRole int         `bson:"issuerRole" json:"issuerRole"`
	Language   string      `bson:"language" json:"language"`
	ExpiresAt  UTCDateTime `bson:"expiresAt" json:"expiresAt"`
	IssuedAt   UTCDateTime `bson:"issuedAt" json:"issuedAt"`
}

type ForgotPasswordRequest struct {
	Email string `json:"email" validate:"required,email"`
}

type ResetPasswordRequest struct {
	Email       string `json:"email" validate:"required,email"`
	Code        string `json:"code" validate:"required,len=6"`
	NewPassword string `json:"newPassword" validate:"required,min=6"`
}

type ValidateEmailRequest struct {
	Code string `json:"code" validate:"required,len=6"`
}
