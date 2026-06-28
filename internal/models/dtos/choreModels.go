package dtos

import (
	"houseflowApi/internal/data/entities"
	"time"
)

type CreateChoreModel struct {
	Title             string              `json:"title" validate:"required,min=3,max=200"`
	Description       string              `json:"description" validate:"required,min=5,max=1000"`
	AssignedTo        string              `json:"assignedTo" validate:"required,len=24"`
	DueDate           UTCDateTime         `json:"dueDate" validate:"required" swaggertype:"string" example:"2026-07-12T00:00:00Z"`
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

type ReviewChoreModel struct {
	ChoreId    string `json:"choreId" validate:"required,len=24"`
	IsApproved *bool  `json:"isApproved" validate:"required"`
}

type ChoreStatusHistoryModel struct {
	Id       string               `json:"id"`
	ChoreId  string               `json:"choreId"`
	Status   entities.ChoreStatus `json:"status"`
	DateTime UTCDateTime          `json:"dateTime"`
	Updater  string               `json:"updater"`
}

type ChoreReviewVoteModel struct {
	Id          string      `json:"id"`
	ChoreId     string      `json:"choreId"`
	HouseId     string      `json:"houseId"`
	ReviewRound int         `json:"reviewRound"`
	ReviewerId  string      `json:"reviewerId"`
	IsApproved  bool        `json:"isApproved"`
	CreatedOn   UTCDateTime `json:"createdOn"`
}

type ChoreResponseModel struct {
	Id                string                    `json:"id"`
	Title             string                    `json:"title"`
	Description       string                    `json:"description"`
	IsCompleted       bool                      `json:"isCompleted"`
	AssignedTo        string                    `json:"assignedTo"`
	DueDate           UTCDateTime               `json:"dueDate"`
	CreatedOn         UTCDateTime               `json:"createdOn"`
	CompletedAt       UTCDateTime               `json:"completedAt"`
	CompletedBy       string                    `json:"completedBy"`
	HouseId           string                    `json:"houseId"`
	HouseOwnerId      string                    `json:"houseOwnerId"`
	Level             entities.ChoreLevel       `json:"level"`
	Status            entities.ChoreStatus      `json:"status"`
	ReviewRound       int                       `json:"reviewRound"`
	IsRecurring       bool                      `json:"isRecurring"`
	RecurringInterval int                       `json:"recurringInterval"`
	StatusHistories   []ChoreStatusHistoryModel `json:"statusHistories"`
	ReviewVotes       []ChoreReviewVoteModel    `json:"reviewVotes"`
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
		ReviewRound:       0,
		IsRecurring:       m.IsRecurring,
		RecurringInterval: m.RecurringInterval,
	}
}

func ChoreToResponseModel(chore entities.Chore, histories []entities.ChoreStatusHistory) ChoreResponseModel {
	return ChoreToResponseModelWithReview(chore, histories, nil)
}

func ChoreToResponseModelWithReview(chore entities.Chore, histories []entities.ChoreStatusHistory, votes []entities.ChoreReviewVote) ChoreResponseModel {
	statusHistories := make([]ChoreStatusHistoryModel, 0, len(histories))
	for _, h := range histories {
		statusHistories = append(statusHistories, ChoreStatusHistoryModel{
			Id:       h.Id.Hex(),
			ChoreId:  h.ChoreId,
			Status:   h.Status,
			DateTime: NewUTCDateTime(h.DateTime),
			Updater:  h.Updater,
		})
	}

	reviewVotes := make([]ChoreReviewVoteModel, 0, len(votes))
	for _, vote := range votes {
		reviewVotes = append(reviewVotes, ChoreReviewVoteModel{
			Id:          vote.Id.Hex(),
			ChoreId:     vote.ChoreId,
			HouseId:     vote.HouseId,
			ReviewRound: vote.ReviewRound,
			ReviewerId:  vote.ReviewerId,
			IsApproved:  vote.IsApproved,
			CreatedOn:   NewUTCDateTime(vote.CreatedOn),
		})
	}

	return ChoreResponseModel{
		Id:                chore.Id.Hex(),
		Title:             chore.Title,
		Description:       chore.Description,
		IsCompleted:       chore.IsCompleted,
		AssignedTo:        chore.AssignedTo,
		DueDate:           NewUTCDateTime(chore.DueDate),
		CreatedOn:         NewUTCDateTime(chore.CreatedOn),
		CompletedAt:       NewUTCDateTime(chore.CompletedAt),
		CompletedBy:       chore.CompletedBy,
		HouseId:           chore.HouseId,
		HouseOwnerId:      chore.HouseOwnerId,
		Level:             chore.Level,
		Status:            chore.Status,
		ReviewRound:       chore.ReviewRound,
		IsRecurring:       chore.IsRecurring,
		RecurringInterval: chore.RecurringInterval,
		StatusHistories:   statusHistories,
		ReviewVotes:       reviewVotes,
	}
}
