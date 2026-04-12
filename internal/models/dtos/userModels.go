package dtos

import (
	"houseflowApi/internal/data/entities"
	"time"
)

type NewUserModel struct {
	Firstname   string `bson:"firstName" json:"firstName" validate:"required,min=2,max=50"`
	Lastname    string `bson:"lastName" json:"lastName" validate:"required,min=2,max=50"`
	PhoneNumber string `bson:"phoneNumber" json:"phoneNumber" validate:"omitempty,min=10,max=15"`
	Email       string `bson:"email" json:"email" validate:"required,email"`
	Password    string `bson:"password" json:"password" validate:"required,min=6"`
	Age         int    `bson:"age" json:"age" validate:"omitempty,gte=0,lte=150"`
}

type SignUpUserModel struct {
	Email     string `bson:"email" json:"email" validate:"required,email"`
	Password  string `bson:"password" json:"password" validate:"required,min=6"`
	Firstname string `bson:"firstName" json:"firstName" validate:"required,min=2,max=50"`
	Lastname  string `bson:"lastName" json:"lastName" validate:"required,min=2,max=50"`
}

// UpdateUserModel — sadece gönderilen (non-nil) alanlar güncellenir.
type UpdateUserModel struct {
	Firstname     *string `json:"firstName,omitempty"`
	Lastname      *string `json:"lastName,omitempty"`
	PhoneNumber   *string `json:"phoneNumber,omitempty" validate:"omitempty,min=10,max=15"`
	Age           *int    `json:"age,omitempty" validate:"omitempty,gte=0,lte=150"`
	ImageURL      *string `json:"imageUrl,omitempty"`
	IsVerifyPhone *bool   `json:"isVerifyPhone,omitempty"`
	IsVerifyEmail *bool   `json:"isVerifyEmail,omitempty"`
}

func (m *NewUserModel) ToEntity() entities.User {
	return entities.User{
		Firstname:           m.Firstname,
		Lastname:            m.Lastname,
		PhoneNumber:         m.PhoneNumber,
		Email:               m.Email,
		HashPassword:        "",
		Age:                 m.Age,
		CreatedOn:           time.Now(),
		UpdatedOn:           time.Now(),
		LastLogin:           time.Now(),
		IsActive:            true,
		IsVerifyPhone:       false,
		IsVerifyEmail:       false,
		FailedLoginAttempts: 0,
		HouseIds:            []string{},
		Role:                entities.Normal,
	}
}

func (m *SignUpUserModel) ToEntity() entities.User {
	return entities.User{
		Email:               m.Email,
		HashPassword:        m.Password,
		Age:                 0,
		PhoneNumber:         "",
		Firstname:           m.Firstname,
		Lastname:            m.Lastname,
		CreatedOn:           time.Now(),
		UpdatedOn:           time.Now(),
		LastLogin:           time.Now(),
		IsActive:            true,
		IsVerifyPhone:       false,
		IsVerifyEmail:       false,
		FailedLoginAttempts: 0,
		HouseIds:            []string{},
		Role:                entities.Normal,
	}
}
