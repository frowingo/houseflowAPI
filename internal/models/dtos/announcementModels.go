package dtos

import (
	"houseflowApi/internal/data/entities"
	"time"
)

type CreateAnnouncementModel struct {
	HouseId     string `json:"houseId" validate:"required,len=24"`
	Title       string `json:"title" validate:"required"`
	Description string `json:"description" validate:"required"`
}

type AnnouncementResponseModel struct {
	Id          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	HouseId     string      `json:"houseId"`
	AnnouncedBy string      `json:"announcedBy"`
	CreatedOn   UTCDateTime `json:"createdOn"`
}

func (m CreateAnnouncementModel) ToEntity(userId string, now time.Time) entities.Announcement {
	return entities.Announcement{
		Title:        m.Title,
		Description:  m.Description,
		UserId:       userId,
		HouseId:      m.HouseId,
		CreatedOn:    now,
		DisplayUntil: now.Add(24 * time.Hour),
	}
}

func AnnouncementToResponseModel(announcement entities.Announcement, announcedBy string) AnnouncementResponseModel {
	return AnnouncementResponseModel{
		Id:          announcement.Id.Hex(),
		Title:       announcement.Title,
		Description: announcement.Description,
		HouseId:     announcement.HouseId,
		AnnouncedBy: announcedBy,
		CreatedOn:   NewUTCDateTime(announcement.CreatedOn),
	}
}
