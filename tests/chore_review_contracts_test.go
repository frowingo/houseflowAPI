package tests

import (
	"encoding/json"
	"strings"
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

func TestChoreResponseIncludesSortedPhotoRoundsWithoutExposingObjectKeys(t *testing.T) {
	chore := entities.Chore{
		Id: primitive.NewObjectID(),
		ReviewPhotoKeys: map[string]string{
			"3":       "houses/private/round-3.jpg",
			"1":       "houses/private/round-1.jpg",
			"invalid": "houses/private/invalid.jpg",
		},
	}

	response := dtos.ChoreToResponseModel(chore, nil)
	if len(response.ReviewPhotoRounds) != 2 || response.ReviewPhotoRounds[0] != 1 || response.ReviewPhotoRounds[1] != 3 {
		t.Fatalf("review photo rounds = %v, want [1 3]", response.ReviewPhotoRounds)
	}
	payload, err := json.Marshal(chore)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "" || containsAny(string(payload), "reviewPhotoKeys", "houses/private") {
		t.Fatalf("chore JSON exposes private review photo keys: %s", payload)
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
