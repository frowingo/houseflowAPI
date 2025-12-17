package entities

import "time"

type User struct {
	Id          string    `json:"id"`
	Firstname   string    `json:"firstName"`
	Lastname    string    `json:"lastName"`
	PhoneNumber string    `json:"phoneNumber"`
	Email       string    `json:"email"`
	Age         int       `json:"age"`
	ImageURL    string    `json:"image_url"`
	HouseIds    []string  `json:"house_ids"`
	IsActive    bool      `json:"is_active"`
	CreatedOn   time.Time `json:"created_on"`
	UpdatedOn   time.Time `json:"updated_on"`
	LastLogin   time.Time `json:"last_login"`
}
