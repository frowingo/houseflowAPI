package dtos

import (
	"houseflowApi/internal/data/entities"
	"time"
)

type NewUserModel struct {
	Firstname   string `bson:"firstName" json:"firstName"`
	Lastname    string `bson:"lastName" json:"lastName"`
	PhoneNumber string `bson:"phoneNumber" json:"phoneNumber"`
	Email       string `bson:"email" json:"email"`
	Password    string `bson:"password" json:"password"`
	Age         int    `bson:"age" json:"age"`
}

type SignUpUserModel struct {
	Email    string `bson:"email" json:"email"`
	Password string `bson:"password" json:"password"`
}

func (m *NewUserModel) ToEntity() entities.User {
	return entities.User{
		Firstname:    m.Firstname,
		Lastname:     m.Lastname,
		PhoneNumber:  m.PhoneNumber,
		Email:        m.Email,
		HashPassword: "",
		Age:          m.Age,
		CreatedOn:    time.Now(),
		UpdatedOn:    time.Now(),
		IsActive:     true,
		HouseIds:     []string{},
	}
}

func (m *SignUpUserModel) ToEntity() entities.User {
	return entities.User{
		Email:        m.Email,
		HashPassword: m.Password,
		Age:          0,
		PhoneNumber:  "",
		Firstname:    "",
		Lastname:     "",
		CreatedOn:    time.Now(),
		UpdatedOn:    time.Now(),
		IsActive:     true,
		HouseIds:     []string{},
	}
}
