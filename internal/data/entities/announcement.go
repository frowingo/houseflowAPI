package entities

import "time"

type Announcement struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	UserId       string    `json:"user_id"`
	HouseId      string    `json:"house_id"`
	CreatedOn    time.Time `json:"created_on"`
	DisplayUntil time.Time `json:"display_until"`
}
