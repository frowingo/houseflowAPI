package dtos

import (
	"houseflowApi/internal/data/entities"
	"time"
)

type LocalizationRequestModel struct {
	Type     string `json:"type" validate:"required"`
	Language string `json:"language" validate:"required"`
	Key      string `json:"key" validate:"required"`
	Value    string `json:"value" validate:"required"`
}

type LocalizationPlaintextResponseModel struct {
	Key      string `json:"key"`
	Language string `json:"language"`
	Value    string `json:"value"`
}

type LocalizationLanguageResponseModel struct {
	Prefix     string `json:"prefix"`
	Name       string `json:"name"`
	NativeName string `json:"nativeName"`
	IsDefault  bool   `json:"isDefault"`
	IsActive   bool   `json:"isActive"`
	Image      string `json:"image"`
}

type LocalizationLanguageRequestModel struct {
	Prefix     string `json:"prefix" validate:"required"`
	Name       string `json:"name" validate:"required"`
	NativeName string `json:"nativeName" validate:"required"`
	IsDefault  bool   `json:"isDefault"`
	IsActive   bool   `json:"isActive"`
	Image      string `json:"image" validate:"required"`
}

func (m LocalizationRequestModel) ToEntity() entities.Localization {
	now := time.Now()
	return entities.Localization{
		Language:  entities.LocalizationLanguage(m.Language),
		Key:       m.Key,
		Type:      entities.LocalizationType(m.Type),
		Value:     m.Value,
		CreatedOn: now,
		UpdatedOn: now,
	}
}

func (m LocalizationLanguageRequestModel) ToEntity() entities.LocalizationLanguageOption {
	now := time.Now()
	return entities.LocalizationLanguageOption{
		Code:       m.Prefix,
		Name:       m.Name,
		NativeName: m.NativeName,
		IsDefault:  m.IsDefault,
		IsActive:   m.IsActive,
		Image:      m.Image,
		CreatedOn:  now,
		UpdatedOn:  now,
	}
}
