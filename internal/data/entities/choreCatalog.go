package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// SystemUserId owns the chore catalog entries every user sees. It is not an
// ObjectID, so catalog code never parses UserId as one.
const SystemUserId = "SYSTEM"

// SystemChoreCatalogKeyPrefix starts the localization keys a system entry
// stores as its title and description.
const SystemChoreCatalogKeyPrefix = "choreCatalog.system."

// ChoreCatalog is a reusable chore template. System entries have
// UserId = SystemUserId; the rest belong to the user who created them.
type ChoreCatalog struct {
	Id                primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title             string             `bson:"title" json:"title"`
	Description       string             `bson:"description" json:"description"`
	RecurringInterval int                `bson:"recurringInterval" json:"recurringInterval"`
	Level             ChoreLevel         `bson:"level" json:"level"`
	Category          ChoreCategory      `bson:"category" json:"category"`
	CreatedOn         time.Time          `bson:"createdOn" json:"createdOn"`
	UpdatedOn         time.Time          `bson:"updatedOn" json:"updatedOn"`
	IsDeleted         bool               `bson:"isDeleted" json:"isDeleted"`
	UserId            string             `bson:"userId" json:"userId"`
}

func (c ChoreCatalog) IsSystem() bool {
	return c.UserId == SystemUserId
}
