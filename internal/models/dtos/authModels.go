package dtos

import "houseflowApi/internal/data/entities"

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
	// SignUpProvider is where the account was created: 0 email/password,
	// 1 Google, 2 Apple, 3 Facebook, 4 Snapchat.
	SignUpProvider  entities.AuthProvider   `json:"signUpProvider"`
	LinkedProviders []entities.AuthProvider `json:"linkedProviders"`
	HasPassword     bool                    `json:"hasPassword"`
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

type SocialAuthRequestModel struct {
	IDToken           string `json:"idToken" validate:"omitempty,max=8192"`
	AccessToken       string `json:"accessToken" validate:"omitempty,max=8192"`
	Nonce             string `json:"nonce" validate:"omitempty,max=256"`
	AuthorizationCode string `json:"authorizationCode" validate:"omitempty,max=1024"`
	Firstname         string `json:"firstName" validate:"omitempty,max=50"`
	Lastname          string `json:"lastName" validate:"omitempty,max=50"`
	Language          string `json:"language" validate:"omitempty,oneof=en tr"`
}

// SocialAuthResponseModel carries our token, or, when the provider shared no
// email, needsEmail and the pendingToken that finishes the sign-up.
type SocialAuthResponseModel struct {
	Token        string `json:"token,omitempty"`
	IsNewUser    bool   `json:"isNewUser"`
	NeedsEmail   bool   `json:"needsEmail,omitempty"`
	PendingToken string `json:"pendingToken,omitempty"`
}

type SocialSignUpCodeRequestModel struct {
	PendingToken string `json:"pendingToken" validate:"required,max=4096"`
	Email        string `json:"email" validate:"required,email,max=254"`
}

type CompleteSocialSignUpRequestModel struct {
	PendingToken string `json:"pendingToken" validate:"required,max=4096"`
	Email        string `json:"email" validate:"required,email,max=254"`
	Code         string `json:"code" validate:"required,len=6"`
	Firstname    string `json:"firstName" validate:"omitempty,max=50"`
	Lastname     string `json:"lastName" validate:"omitempty,max=50"`
	Language     string `json:"language" validate:"omitempty,oneof=en tr"`
}

// ProviderDataDeletionResult is the outcome of a provider's data deletion
// request. UserID and HouseIDs are only for change notifications.
type ProviderDataDeletionResult struct {
	ConfirmationCode string   `json:"confirmationCode"`
	UserID           string   `json:"-"`
	HouseIDs         []string `json:"-"`
}

// FacebookDataDeletionResponseModel is the reply Facebook expects from the
// data deletion callback.
type FacebookDataDeletionResponseModel struct {
	URL              string `json:"url"`
	ConfirmationCode string `json:"confirmation_code"`
}

type DataDeletionStatusResponseModel struct {
	ConfirmationCode string      `json:"confirmationCode"`
	Status           string      `json:"status"`
	RequestedOn      UTCDateTime `json:"requestedOn"`
}

type LinkedProvidersResponseModel struct {
	LinkedProviders []entities.AuthProvider `json:"linkedProviders"`
}

type AppleNotificationRequestModel struct {
	Payload string `json:"payload"`
}
