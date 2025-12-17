package entities

import "time"

type House struct {
	Id         string    `json:"id"`
	OwnerId    string    `json:"owner_id"`
	InviteCode string    `json:"invite_code"`
	Name       string    `json:"name"`
	Type       HouseType `json:"type"`
	MemberIds  []string  `json:"member_ids"`
	CreatedOn  time.Time `json:"created_on"`
	UpdatedOn  time.Time `json:"updated_on"`
}

type HouseType string

const (
	StudentHouse HouseType = "StudentHouse"
	SharedHouse  HouseType = "SharedHouse"
	DormRoom     HouseType = "DormRoom"
)
