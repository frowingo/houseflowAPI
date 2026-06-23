package tests

import (
	"testing"

	"houseflowApi/external/validator"
	"houseflowApi/internal/models/dtos"
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
