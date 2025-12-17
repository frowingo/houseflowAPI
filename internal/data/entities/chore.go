package entities

import "time"

type Chore struct {
	ID                string     `json:"id"`
	Title             string     `json:"title"`
	Description       string     `json:"description"`
	IsCompleted       bool       `json:"is_completed"`
	AssignedTo        string     `json:"assigned_to"`
	DueDate           time.Time  `json:"due_date"`
	CreatedOn         time.Time  `json:"created_on"`
	HouseId           string     `json:"house_id"`
	HouseOwnerId      string     `json:"house_owner_id"`
	Level             ChoreLevel `json:"level"`
	IsRecurring       bool       `json:"is_recurring"`
	RecurringInterval int        `json:"recurring_interval"`
}

type ChoreLevel string

const (
	Easy   ChoreLevel = "Easy"
	Medium ChoreLevel = "Medium"
	Hard   ChoreLevel = "Hard"
)
