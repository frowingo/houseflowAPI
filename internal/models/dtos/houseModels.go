package dtos

import (
	"houseflowApi/internal/data/entities"
)

type UserResultModel struct {
	Id            string      `json:"id"`
	Firstname     string      `json:"firstName"`
	Lastname      string      `json:"lastName"`
	PhoneNumber   string      `json:"phoneNumber"`
	Email         string      `json:"email"`
	BirthDay      UTCDateTime `json:"birthDay"`
	ImageURL      string      `json:"imageUrl"`
	Language      string      `json:"language"`
	HouseIds      []string    `json:"houseIds"`
	IsActive      bool        `json:"isActive"`
	IsVerifyPhone bool        `json:"isVerifyPhone"`
	IsVerifyEmail bool        `json:"isVerifyEmail"`
	CreatedOn     UTCDateTime `json:"createdOn"`
	UpdatedOn     UTCDateTime `json:"updatedOn"`
	LastLogin     UTCDateTime `json:"lastLogin"`
}

type HouseDetailsModel struct {
	Id             string                      `json:"id"`
	OwnerId        string                      `json:"ownerId"`
	Name           string                      `json:"name"`
	Type           entities.HouseType          `json:"type" swaggertype:"integer" enums:"1,2,3"`
	Members        []UserResultModel           `json:"members"`
	MaxMemberCount int                         `json:"maxMemberCount"`
	ProfileImage   string                      `json:"profileImage"`
	CreatedOn      UTCDateTime                 `json:"createdOn"`
	UpdatedOn      UTCDateTime                 `json:"updatedOn"`
	Chores         []ChoreResponseModel        `json:"chores"`
	Announcements  []AnnouncementResponseModel `json:"announcements"`
}

func UserToResultModel(u entities.User) UserResultModel {
	return UserResultModel{
		Id:            u.Id.Hex(),
		Firstname:     u.Firstname,
		Lastname:      u.Lastname,
		PhoneNumber:   u.PhoneNumber,
		Email:         u.Email,
		BirthDay:      NewUTCDateTime(u.BirthDay),
		ImageURL:      u.ImageURL,
		Language:      u.Language,
		HouseIds:      u.HouseIds,
		IsActive:      u.IsActive,
		IsVerifyPhone: u.IsVerifyPhone,
		IsVerifyEmail: u.IsVerifyEmail,
		CreatedOn:     NewUTCDateTime(u.CreatedOn),
		UpdatedOn:     NewUTCDateTime(u.UpdatedOn),
		LastLogin:     NewUTCDateTime(u.LastLogin),
	}
}

func UserToAuthResultModel(user entities.User, houses []entities.House) AuthUserResultModel {
	houseList := make([]AuthHouseModel, 0, len(houses))
	for _, house := range houses {
		houseList = append(houseList, AuthHouseModel{
			HouseID:      house.Id.Hex(),
			HouseName:    house.Name,
			HouseProfile: house.ProfileImage,
		})
	}

	return AuthUserResultModel{
		Id:            user.Id.Hex(),
		Firstname:     user.Firstname,
		Lastname:      user.Lastname,
		PhoneNumber:   user.PhoneNumber,
		Email:         user.Email,
		BirthDay:      NewUTCDateTime(user.BirthDay),
		ImageURL:      user.ImageURL,
		Language:      user.Language,
		HouseList:     houseList,
		IsActive:      user.IsActive,
		IsVerifyPhone: user.IsVerifyPhone,
		IsVerifyEmail: user.IsVerifyEmail,
		CreatedOn:     NewUTCDateTime(user.CreatedOn),
		UpdatedOn:     NewUTCDateTime(user.UpdatedOn),
		LastLogin:     NewUTCDateTime(user.LastLogin),
	}
}

type CreateHouseModel struct {
	OwnerId        string             `json:"ownerId"`
	Name           string             `json:"name" validate:"required,min=3,max=100"`
	Type           entities.HouseType `json:"type" validate:"required,oneof=1 2 3" swaggertype:"integer" enums:"1,2,3" example:"1"`
	MaxMemberCount int                `json:"maxMemberCount" validate:"required,gte=1,lte=8"`
}

type JoinHouseByCodeModel struct {
	InviteCode string `json:"inviteCode" validate:"required,len=8,alphanum"`
}

type GenerateHouseInviteCodeModel struct {
	HouseId string `json:"houseId" validate:"required,len=24,alphanum"`
}

type HouseInviteCodeResponseModel struct {
	InviteCode       string `json:"inviteCode"`
	ExpiresInSeconds int    `json:"expiresInSeconds"`
}

type HouseResponseModel struct {
	Id             string             `json:"id"`
	OwnerId        string             `json:"ownerId"`
	Name           string             `json:"name"`
	Type           entities.HouseType `json:"type" swaggertype:"integer" enums:"1,2,3"`
	MemberIds      []string           `json:"memberIds"`
	MaxMemberCount int                `json:"maxMemberCount"`
	ProfileImage   string             `json:"profileImage"`
	CreatedOn      UTCDateTime        `json:"createdOn"`
	UpdatedOn      UTCDateTime        `json:"updatedOn"`
}

func HouseToResponseModel(house entities.House) HouseResponseModel {
	return HouseResponseModel{
		Id:             house.Id.Hex(),
		OwnerId:        house.OwnerId,
		Name:           house.Name,
		Type:           house.Type,
		MemberIds:      house.MemberIds,
		MaxMemberCount: house.MaxMemberCount,
		ProfileImage:   house.ProfileImage,
		CreatedOn:      NewUTCDateTime(house.CreatedOn),
		UpdatedOn:      NewUTCDateTime(house.UpdatedOn),
	}
}
