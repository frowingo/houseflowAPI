package dtos

import (
	"houseflowApi/internal/data/entities"
	"time"
)

type UserResultModel struct {
	Id            string    `json:"id"`
	Firstname     string    `json:"firstName"`
	Lastname      string    `json:"lastName"`
	PhoneNumber   string    `json:"phoneNumber"`
	Email         string    `json:"email"`
	Age           int       `json:"age"`
	ImageURL      string    `json:"imageUrl"`
	HouseIds      []string  `json:"houseIds"`
	IsActive      bool      `json:"isActive"`
	IsVerifyPhone bool      `json:"isVerifyPhone"`
	IsVerifyEmail bool      `json:"isVerifyEmail"`
	CreatedOn     time.Time `json:"createdOn"`
	UpdatedOn     time.Time `json:"updatedOn"`
	LastLogin     time.Time `json:"lastLogin"`
}

type HouseDetailsModel struct {
	Id             string               `json:"id"`
	OwnerId        string               `json:"ownerId"`
	InviteCode     string               `json:"inviteCode"`
	Name           string               `json:"name"`
	Type           entities.HouseType   `json:"type" swaggertype:"integer" enums:"1,2,3"`
	Members        []UserResultModel    `json:"members"`
	MaxMemberCount int                  `json:"maxMemberCount"`
	ProfileImage   string               `json:"profileImage"`
	CreatedOn      time.Time            `json:"createdOn"`
	UpdatedOn      time.Time            `json:"updatedOn"`
	Chores         []ChoreResponseModel `json:"chores"`
}

func UserToResultModel(u entities.User) UserResultModel {
	return UserResultModel{
		Id:            u.Id.Hex(),
		Firstname:     u.Firstname,
		Lastname:      u.Lastname,
		PhoneNumber:   u.PhoneNumber,
		Email:         u.Email,
		Age:           u.Age,
		ImageURL:      u.ImageURL,
		HouseIds:      u.HouseIds,
		IsActive:      u.IsActive,
		IsVerifyPhone: u.IsVerifyPhone,
		IsVerifyEmail: u.IsVerifyEmail,
		CreatedOn:     u.CreatedOn,
		UpdatedOn:     u.UpdatedOn,
		LastLogin:     u.LastLogin,
	}
}

type CreateHouseModel struct {
	OwnerId        string             `json:"ownerId" validate:"required,len=24"`
	Name           string             `json:"name" validate:"required,min=3,max=100"`
	Type           entities.HouseType `json:"type" validate:"required,oneof=1 2 3" swaggertype:"integer" enums:"1,2,3" example:"1"`
	MaxMemberCount int                `json:"maxMemberCount" validate:"required,gte=1,lte=8"`
}

type JoinHouseByCodeModel struct {
	UserId     string `json:"userId"`
	InviteCode string `json:"inviteCode" validate:"required,len=8,alphanum"`
}

type HouseResponseModel struct {
	Id             string             `json:"id"`
	OwnerId        string             `json:"ownerId"`
	InviteCode     string             `json:"inviteCode"`
	Name           string             `json:"name"`
	Type           entities.HouseType `json:"type" swaggertype:"integer" enums:"1,2,3"`
	MemberIds      []string           `json:"memberIds"`
	MaxMemberCount int                `json:"maxMemberCount"`
	ProfileImage   string             `json:"profileImage"`
	CreatedOn      time.Time          `json:"createdOn"`
	UpdatedOn      time.Time          `json:"updatedOn"`
}

func (m *CreateHouseModel) ToEntity(inviteCode string) entities.House {
	return entities.House{
		OwnerId:        m.OwnerId,
		InviteCode:     inviteCode,
		Name:           m.Name,
		Type:           m.Type,
		MemberIds:      []string{m.OwnerId},
		MaxMemberCount: m.MaxMemberCount,
		CreatedOn:      time.Now(),
		UpdatedOn:      time.Now(),
	}
}

func HouseToResponseModel(house entities.House) HouseResponseModel {
	return HouseResponseModel{
		Id:             house.Id.Hex(),
		OwnerId:        house.OwnerId,
		InviteCode:     house.InviteCode,
		Name:           house.Name,
		Type:           house.Type,
		MemberIds:      house.MemberIds,
		MaxMemberCount: house.MaxMemberCount,
		ProfileImage:   house.ProfileImage,
		CreatedOn:      house.CreatedOn,
		UpdatedOn:      house.UpdatedOn,
	}
}
