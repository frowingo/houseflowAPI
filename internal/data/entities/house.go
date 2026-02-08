package entities

import "time"

type House struct {
	Id             string    `json:"id"`
	OwnerId        string    `json:"ownerId"`
	InviteCode     string    `json:"inviteCode"`
	Name           string    `json:"name"`
	Type           HouseType `json:"type"`
	MemberIds      []string  `json:"memberIds"`
	MaxMemberCount int       `json:"maxMemberCount"`
	CreatedOn      time.Time `json:"createdOn"`
	UpdatedOn      time.Time `json:"updatedOn"`
}

type HouseType string

const (
	StudentHouse HouseType = "StudentHouse"
	SharedHouse  HouseType = "SharedHouse"
	DormRoom     HouseType = "DormRoom"
)
