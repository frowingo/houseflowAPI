package dtos

import (
	"houseflowApi/internal/data/entities"
	"time"
)

type CreateHouseModel struct {
	OwnerId        string             `json:"ownerId" validate:"required,len=24"`
	Name           string             `json:"name" validate:"required,min=3,max=100"`
	Type           entities.HouseType `json:"type" validate:"required,oneof=StudentHouse SharedHouse DormRoom"`
	MaxMemberCount int                `json:"maxMemberCount" validate:"required,gte=1,lte=8"`
}

type JoinHouseByCodeModel struct {
	UserId     string `json:"userId" validate:"required,len=24"`
	InviteCode string `json:"inviteCode" validate:"required,len=8,alphanum"`
}

type HouseResponseModel struct {
	Id             string             `json:"id"`
	OwnerId        string             `json:"ownerId"`
	InviteCode     string             `json:"inviteCode"`
	Name           string             `json:"name"`
	Type           entities.HouseType `json:"type"`
	MemberIds      []string           `json:"memberIds"`
	MaxMemberCount int                `json:"maxMemberCount"`
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
		Id:             house.Id,
		OwnerId:        house.OwnerId,
		InviteCode:     house.InviteCode,
		Name:           house.Name,
		Type:           house.Type,
		MemberIds:      house.MemberIds,
		MaxMemberCount: house.MaxMemberCount,
		CreatedOn:      house.CreatedOn,
		UpdatedOn:      house.UpdatedOn,
	}
}
