package abstract

import "houseflowApi/internal/data/entities"

// Cache is the in-process localization cache boundary used by application handlers.
type Cache interface {
	GetPlaintexts(language string) (map[string]string, bool)
	MergePlaintexts(language string, values map[string]string)
	SetLocalization(localization entities.Localization)
}
