package dtos

import (
	"houseflowApi/internal/data/entities"
	"time"
)

type NewUserModel struct {
	Firstname   string      `bson:"firstName" json:"firstName" validate:"required,min=2,max=50"`
	Lastname    string      `bson:"lastName" json:"lastName" validate:"required,min=2,max=50"`
	PhoneNumber string      `bson:"phoneNumber" json:"phoneNumber" validate:"omitempty,min=10,max=15"`
	Email       string      `bson:"email" json:"email" validate:"required,email"`
	Password    string      `bson:"password" json:"password" validate:"required,min=6"`
	BirthDay    UTCDateTime `bson:"birthDay" json:"birthDay" validate:"omitempty"`
}

type SignUpUserModel struct {
	Email     string `bson:"email" json:"email" validate:"required,email"`
	Password  string `bson:"password" json:"password" validate:"required,min=6"`
	Firstname string `bson:"firstName" json:"firstName" validate:"required,min=2,max=50"`
	Lastname  string `bson:"lastName" json:"lastName" validate:"required,min=2,max=50"`
}

// UpdateUserModel — sadece gönderilen (non-nil) alanlar güncellenir.
type UpdateUserModel struct {
	Firstname   *string      `json:"firstName,omitempty"`
	Lastname    *string      `json:"lastName,omitempty"`
	PhoneNumber *string      `json:"phoneNumber,omitempty" validate:"omitempty,min=10,max=15"`
	BirthDay    *UTCDateTime `json:"birthDay,omitempty"`
	ImageURL    *string      `json:"imageUrl,omitempty"`
}

func (m *NewUserModel) ToEntity() entities.User {
	return entities.User{
		Firstname:           m.Firstname,
		Lastname:            m.Lastname,
		PhoneNumber:         m.PhoneNumber,
		Email:               m.Email,
		HashPassword:        "",
		BirthDay:            m.BirthDay.Time,
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
