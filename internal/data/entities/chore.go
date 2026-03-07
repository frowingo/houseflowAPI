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
	IsRecurring       bool               `bson:"isRecurring" json:"isRecurring"`
	RecurringInterval int                `bson:"recurringInterval" json:"recurringInterval"`
}

type ChoreLevel int

const (
	Easy   ChoreLevel = 10
	Medium ChoreLevel = 20
	Hard   ChoreLevel = 30
)

type ChoreStatus int

const (
	Draft     ChoreStatus = 0
	Progress  ChoreStatus = 1
	InTest    ChoreStatus = 2
	Completed ChoreStatus = 3
)
