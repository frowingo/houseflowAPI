package dtos

import (
	"fmt"
	"houseflowApi/internal/data/entities"
	"strings"
	"time"
)

var flexibleTimeFormats = []string{
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

type FlexibleTime struct {
	time.Time
}

func (ft *FlexibleTime) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	for _, format := range flexibleTimeFormats {
		t, err := time.Parse(format, s)
		if err == nil {
			ft.Time = t
			return nil
		}
	}
	return fmt.Errorf("cannot parse %q as time, expected formats: RFC3339 or \"2006-01-02 15:04:05\"", s)
}

type CreateChoreModel struct {
	Title             string              `json:"title" validate:"required,min=3,max=200"`
	Description       string              `json:"description" validate:"required,min=5,max=1000"`
	AssignedTo        string              `json:"assignedTo" validate:"required,len=24"`
	DueDate           FlexibleTime        `json:"dueDate" validate:"required" swaggertype:"string" example:"2026-07-12 00:00:00"`
	HouseId           string              `json:"houseId" validate:"required,len=24"`
	Level             entities.ChoreLevel `json:"level" validate:"required,oneof=10 20 30"`
	IsRecurring       bool                `json:"isRecurring"`
	RecurringInterval int                 `json:"recurringInterval" validate:"omitempty,gte=1,lte=365"`
}

type UpdateChoreStatusModel struct {
	ChoreId string               `json:"choreId" validate:"required,len=24"`
	Status  entities.ChoreStatus `json:"status" validate:"required,oneof=0 1 2 3"`
}

type BulkUpdateChoreStatusModel struct {
	HouseId string                   `json:"houseId" validate:"required,len=24"`
	Chores  []UpdateChoreStatusModel `json:"chores" validate:"required,min=1,dive"`
}

type ChoreStatusHistoryModel struct {
	Id       string               `json:"id"`
	ChoreId  string               `json:"choreId"`
	Status   entities.ChoreStatus `json:"status"`
	DateTime time.Time            `json:"dateTime"`
	Updater  string               `json:"updater"`
}

type ChoreResponseModel struct {
	Id                string                    `json:"id"`
	Title             string                    `json:"title"`
	Description       string                    `json:"description"`
	IsCompleted       bool                      `json:"isCompleted"`
	AssignedTo        string                    `json:"assignedTo"`
	DueDate           time.Time                 `json:"dueDate"`
	CreatedOn         time.Time                 `json:"createdOn"`
	CompletedAt       time.Time                 `json:"completedAt"`
	CompletedBy       string                    `json:"completedBy"`
	HouseId           string                    `json:"houseId"`
	HouseOwnerId      string                    `json:"houseOwnerId"`
	Level             entities.ChoreLevel       `json:"level"`
	Status            entities.ChoreStatus      `json:"status"`
	IsRecurring       bool                      `json:"isRecurring"`
	RecurringInterval int                       `json:"recurringInterval"`
	StatusHistories   []ChoreStatusHistoryModel `json:"statusHistories"`
}

func (m *CreateChoreModel) ToEntity(houseOwnerId string) entities.Chore {
	return entities.Chore{
		Title:             m.Title,
		Description:       m.Description,
		IsCompleted:       false,
		AssignedTo:        m.AssignedTo,
		DueDate:           m.DueDate.Time,
		CreatedOn:         time.Now(),
		HouseId:           m.HouseId,
		HouseOwnerId:      houseOwnerId,
		Level:             m.Level,
		Status:            entities.Draft,
		IsRecurring:       m.IsRecurring,
		RecurringInterval: m.RecurringInterval,
	}
}

func ChoreToResponseModel(chore entities.Chore, histories []entities.ChoreStatusHistory) ChoreResponseModel {
	statusHistories := make([]ChoreStatusHistoryModel, 0, len(histories))
	for _, h := range histories {
		statusHistories = append(statusHistories, ChoreStatusHistoryModel{
			Id:       h.Id.Hex(),
			ChoreId:  h.ChoreId,
			Status:   h.Status,
			DateTime: h.DateTime,
			Updater:  h.Updater,
		})
	}
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
		StatusHistories:   statusHistories,
	}
}
