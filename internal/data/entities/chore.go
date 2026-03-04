package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Chore struct {
	Id                primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title             string             `bson:"title" json:"title"`
	Description       string             `bson:"description" json:"description"`
	IsCompleted       bool               `bson:"is_completed" json:"is_completed"`
	AssignedTo        string             `bson:"assigned_to" json:"assigned_to"`
	DueDate           time.Time          `bson:"due_date" json:"due_date"`
	CreatedOn         time.Time          `bson:"created_on" json:"created_on"`
	HouseId           string             `bson:"house_id" json:"house_id"`
	HouseOwnerId      string             `bson:"house_owner_id" json:"house_owner_id"`
	Level             ChoreLevel         `bson:"level" json:"level"`
	Status            ChoreStatus        `bson:"status" json:"status"`
	IsRecurring       bool               `bson:"is_recurring" json:"is_recurring"`
	RecurringInterval int                `bson:"recurring_interval" json:"recurring_interval"`
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
