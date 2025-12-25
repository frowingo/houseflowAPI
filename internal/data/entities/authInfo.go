package entities

import "time"

type AuthInfo struct {
	Id          string    `bson:"_id,omitempty" json:"id"`
	UserId      string    `bson:"userId" json:"userId"`
	AccessToken string    `bson:"accessToken" json:"accessToken"`
	RefreshDate time.Time `bson:"refreshDate" json:"refreshDate"`
}
