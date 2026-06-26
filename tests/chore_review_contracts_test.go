package tests

import (
	"testing"
	"time"

	"houseflowApi/external/validator"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestReviewChoreModelAcceptsRejectedVote(t *testing.T) {
	isApproved := false
	model := dtos.ReviewChoreModel{
		ChoreId:    "507f1f77bcf86cd799439011",
		IsApproved: &isApproved,
	}

	if err := validator.NewValidator().Validate(model); err != nil {
		t.Fatalf("reject vote must be a valid review payload: %v", err)
	}
}

func TestReviewChoreModelRequiresVoteValue(t *testing.T) {
	model := dtos.ReviewChoreModel{
		ChoreId: "507f1f77bcf86cd799439011",
	}

	if err := validator.NewValidator().Validate(model); err == nil {
		t.Fatal("review payload without vote value must be invalid")
	}
}

func TestChoreResponseIncludesReviewVotesFromAllRounds(t *testing.T) {
	choreId := primitive.NewObjectID()
	chore := entities.Chore{
		Id:          choreId,
		Title:       "Clean kitchen",
		Description: "Clean kitchen surfaces",
		AssignedTo:  primitive.NewObjectID().Hex(),
		DueDate:     time.Now(),
		CreatedOn:   time.Now(),
		HouseId:     primitive.NewObjectID().Hex(),
		ReviewRound: 3,
		Status:      entities.Completed,
	}
	votes := []entities.ChoreReviewVote{
		{
			Id:          primitive.NewObjectID(),
			ChoreId:     choreId.Hex(),
			HouseId:     chore.HouseId,
			ReviewRound: 1,
			ReviewerId:  primitive.NewObjectID().Hex(),
			IsApproved:  false,
			CreatedOn:   time.Now(),
		},
		{
			Id:          primitive.NewObjectID(),
			ChoreId:     choreId.Hex(),
			HouseId:     chore.HouseId,
			ReviewRound: 3,
			ReviewerId:  primitive.NewObjectID().Hex(),
			IsApproved:  false,
			CreatedOn:   time.Now(),
		},
	}

	response := dtos.ChoreToResponseModelWithReview(chore, nil, votes)

	if len(response.ReviewVotes) != len(votes) {
		t.Fatalf("expected all review votes to be returned, got %d", len(response.ReviewVotes))
	}
	if response.ReviewVotes[0].ReviewRound != 1 || response.ReviewVotes[1].ReviewRound != 3 {
		t.Fatalf("expected review votes from every round, got %+v", response.ReviewVotes)
	}
}
