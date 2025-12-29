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
