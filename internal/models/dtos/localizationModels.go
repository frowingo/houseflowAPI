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
