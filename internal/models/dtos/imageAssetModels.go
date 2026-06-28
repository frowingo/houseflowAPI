package dtos

import (
	"houseflowApi/internal/data/entities"
	"strings"
	"time"
)

type CreateImageAssetModel struct {
	Category string `json:"category" validate:"required"`
	FileName string `json:"fileName" validate:"required"`
	FileURL  string `json:"fileURL" validate:"required"`
	IsActive bool   `json:"isActive"`
}

func (m *CreateImageAssetModel) ToEntity() entities.ImageAsset {
	return entities.ImageAsset{
		Category:  m.Category,
		FileName:  m.FileName,
		FileURL:   m.FileURL,
		PublicID:  strings.ToLower(m.Category) + "_" + strings.ToLower(m.FileName),
		IsActive:  m.IsActive,
		CreatedOn: time.Now(),
		UpdatedOn: time.Now(),
	}
}

type UpdateImageAssetModel struct {
	PublicID string  `json:"publicId" validate:"required"`
	FileURL  *string `json:"fileURL,omitempty"`
	IsActive *bool   `json:"isActive,omitempty"`
}

// ImageAssetResultModel - Id ve Category alanları hariç tutulmuştur.
type ImageAssetResultModel struct {
	FileName  string      `json:"fileName"`
	FileURL   string      `json:"fileURL"`
	PublicID  string      `json:"publicId"`
	CreatedOn UTCDateTime `json:"createdOn"`
	UpdatedOn UTCDateTime `json:"updatedOn"`
}

func ToImageAssetResultModel(e entities.ImageAsset) ImageAssetResultModel {
	return ImageAssetResultModel{
		FileName:  e.FileName,
		FileURL:   e.FileURL,
		PublicID:  e.PublicID,
		CreatedOn: NewUTCDateTime(e.CreatedOn),
		UpdatedOn: NewUTCDateTime(e.UpdatedOn),
	}
}
