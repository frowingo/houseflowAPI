package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Chore struct {
	Id                primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title             string             `bson:"title" json:"title"`
	Description       string             `bson:"description" json:"description"`
	IsCompleted       bool               `bson:"isCompleted" json:"isCompleted"`
	AssignedTo        string             `bson:"assignedTo" json:"assignedTo"`
	DueDate           time.Time          `bson:"dueDate" json:"dueDate"`
	CreatedOn         time.Time          `bson:"createdOn" json:"createdOn"`
	CompletedAt       time.Time          `bson:"completedAt" json:"completedAt"`
	CompletedBy       string             `bson:"completedBy" json:"completedBy"`
	HouseId           string             `bson:"houseId" json:"houseId"`
	HouseOwnerId      string             `bson:"houseOwnerId" json:"houseOwnerId"`
	Level             ChoreLevel         `bson:"level" json:"level"`
	Status            ChoreStatus        `bson:"status" json:"status"`
	ReviewRound       int                `bson:"reviewRound" json:"reviewRound"`
	ReviewPhotoKeys   map[string]string  `bson:"reviewPhotoKeys,omitempty" json:"-"`
	IsRecurring       bool               `bson:"isRecurring" json:"isRecurring"`
	RecurringInterval int                `bson:"recurringInterval" json:"recurringInterval"`
	Category          ChoreCategory      `bson:"category" json:"category"`
	Version           int64              `bson:"version" json:"-"`
}

type ChoreLevel int

const (
	Easy   ChoreLevel = 10
	Medium ChoreLevel = 20
	Hard   ChoreLevel = 30
)

// ChoreCategory groups chores and chore catalog entries. Zero means unset:
// chores created before categories existed decode as zero.
type ChoreCategory int

const (
	ChoreCategoryKitchen        ChoreCategory = 1
	ChoreCategoryBathLaundry    ChoreCategory = 2
	ChoreCategoryToilet         ChoreCategory = 3
	ChoreCategoryRooms          ChoreCategory = 4
	ChoreCategoryFloorsGeneral  ChoreCategory = 5
	ChoreCategoryPets           ChoreCategory = 6
	ChoreCategoryTrashRecycling ChoreCategory = 7
	ChoreCategoryPlantsBalcony  ChoreCategory = 8
	ChoreCategoryShoppingHome   ChoreCategory = 9
)

type ChoreStatus int

const (
	Draft     ChoreStatus = 0
	Progress  ChoreStatus = 1
	InTest    ChoreStatus = 2
	Completed ChoreStatus = 3
	Failed    ChoreStatus = 4
)
