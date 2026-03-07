package dtos

import (
	"houseflowApi/internal/data/entities"
	"time"
)

type CreateChoreModel struct {
	Title             string              `json:"title" validate:"required,min=3,max=200"`
	Description       string              `json:"description" validate:"required,min=5,max=1000"`
	AssignedTo        string              `json:"assignedTo" validate:"required,len=24"`
	DueDate           time.Time           `json:"dueDate" validate:"required"`
	HouseId           string              `json:"houseId" validate:"required,len=24"`
	Level             entities.ChoreLevel `json:"level" validate:"required,oneof=10,20,30"`
	IsRecurring       bool                `json:"isRecurring"`
	RecurringInterval int                 `json:"recurringInterval" validate:"omitempty,gte=1,lte=365"`
}

type UpdateChoreStatusModel struct {
	ChoreId string               `json:"choreId" validate:"required,len=24"`
	Status  entities.ChoreStatus `json:"status" validate:"required,oneof=0,1,2,3"`
}

type BulkUpdateChoreStatusModel []UpdateChoreStatusModel

type ChoreResponseModel struct {
	Id                string               `json:"id"`
	Title             string               `json:"title"`
	Description       string               `json:"description"`
	IsCompleted       bool                 `json:"isCompleted"`
	AssignedTo        string               `json:"assignedTo"`
	DueDate           time.Time            `json:"dueDate"`
	CreatedOn         time.Time            `json:"createdOn"`
	CompletedAt       time.Time            `json:"completedAt"`
	CompletedBy       string               `json:"completedBy"`
	HouseId           string               `json:"houseId"`
	HouseOwnerId      string               `json:"houseOwnerId"`
	Level             entities.ChoreLevel  `json:"level"`
	Status            entities.ChoreStatus `json:"status"`
	IsRecurring       bool                 `json:"isRecurring"`
	RecurringInterval int                  `json:"recurringInterval"`
}

func (m *CreateChoreModel) ToEntity(houseOwnerId string) entities.Chore {
	return entities.Chore{
		Title:             m.Title,
		Description:       m.Description,
		IsCompleted:       false,
		AssignedTo:        m.AssignedTo,
		DueDate:           m.DueDate,
		CreatedOn:         time.Now(),
		HouseId:           m.HouseId,
		HouseOwnerId:      houseOwnerId,
		Level:             m.Level,
		Status:            entities.Draft,
		IsRecurring:       m.IsRecurring,
		RecurringInterval: m.RecurringInterval,
	}
}

func ChoreToResponseModel(chore entities.Chore) ChoreResponseModel {
	return ChoreResponseModel{
		Id:                chore.Id.Hex(),
		Title:             chore.Title,
		Description:       chore.Description,
		IsCompleted:       chore.IsCompleted,
		AssignedTo:        chore.AssignedTo,
		DueDate:           chore.DueDate,
		CreatedOn:         chore.CreatedOn,
		CompletedAt:       chore.CompletedAt,
		CompletedBy:       chore.CompletedBy,
		HouseId:           chore.HouseId,
		HouseOwnerId:      chore.HouseOwnerId,
		Level:             chore.Level,
		Status:            chore.Status,
		IsRecurring:       chore.IsRecurring,
		RecurringInterval: chore.RecurringInterval,
	}
}
