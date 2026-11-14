package dtos

import "houseflowApi/internal/data/entities"

// ChoreCatalogModel is both an upsert request item and a response item, so the
// client can patch its list with what it gets back. UserId, CreatedOn and
// UpdatedOn are filled by the server and ignored in requests.
type ChoreCatalogModel struct {
	Id                string                 `json:"id,omitempty" validate:"omitempty,len=24"`
	Title             string                 `json:"title" validate:"required,min=3,max=200"`
	Description       string                 `json:"description" validate:"max=1000"`
	RecurringInterval int                    `json:"recurringInterval" validate:"gte=0,lte=365"`
	Level             entities.ChoreLevel    `json:"level" validate:"required,oneof=10 20 30"`
	Category          entities.ChoreCategory `json:"category" validate:"required,oneof=1 2 3 4 5 6 7 8 9"`
	// IsDeleted true on an existing entry soft-deletes it.
	IsDeleted bool `json:"isDeleted"`
	// IsSystem true on a new entry creates a system entry (SuperAdmin only).
	IsSystem  bool        `json:"isSystem"`
	UserId    string      `json:"userId,omitempty"`
	CreatedOn UTCDateTime `json:"createdOn"`
	UpdatedOn UTCDateTime `json:"updatedOn"`
}

// MaxChoreCatalogUpsertItems bounds one upsert request.
const MaxChoreCatalogUpsertItems = 100

// ChoreCatalogUpsertModel wraps the request array so it can be validated.
type ChoreCatalogUpsertModel struct {
	Items []ChoreCatalogModel `validate:"required,min=1,max=100,dive"`
}

func ChoreCatalogToModel(catalog entities.ChoreCatalog) ChoreCatalogModel {
	return ChoreCatalogModel{
		Id:                catalog.Id.Hex(),
		Title:             catalog.Title,
		Description:       catalog.Description,
		RecurringInterval: catalog.RecurringInterval,
		Level:             catalog.Level,
		Category:          catalog.Category,
		IsDeleted:         catalog.IsDeleted,
		IsSystem:          catalog.IsSystem(),
		UserId:            catalog.UserId,
		CreatedOn:         NewUTCDateTime(catalog.CreatedOn),
		UpdatedOn:         NewUTCDateTime(catalog.UpdatedOn),
	}
}
