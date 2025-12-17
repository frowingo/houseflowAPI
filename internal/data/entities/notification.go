package entities

import "time"

type Notification struct {
	Id           string    `json:"id"`
	Title        string    `json:"title"`
	Message      string    `json:"message"`
	CreatedAt    time.Time `json:"created_at"`
	HouseId      string    `json:"house_id"`
	HouseOwnerId string    `json:"house_owner_id"`
}
