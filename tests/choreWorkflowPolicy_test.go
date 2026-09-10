package tests

import (
	"testing"
	"time"

	chorePolicies "houseflowApi/internal/application/chore/policies"
	"houseflowApi/internal/data/entities"
)

func TestChoreWorkflowAdvancesOnlyThroughValidStatuses(t *testing.T) {
	policy := chorePolicies.NewWorkflowPolicy()
	now := time.Now()
	chore := entities.Chore{AssignedTo: "assignee", Status: entities.Draft}

	if err := policy.Advance(&chore, entities.Progress, 2, "assignee", now); err != nil {
		t.Fatal(err)
	}
	if chore.Status != entities.Progress || chore.IsCompleted {
		t.Fatalf("unexpected chore state after advance: %+v", chore)
	}
	if err := policy.Advance(&chore, entities.Completed, 2, "assignee", now); err == nil {
		t.Fatal("expected invalid transition error")
	}
}

func TestChoreWorkflowCompletesSingleMemberHouseWithoutReview(t *testing.T) {
	policy := chorePolicies.NewWorkflowPolicy()
	now := time.Now()
	chore := entities.Chore{AssignedTo: "assignee", Status: entities.Progress}

	if err := policy.Advance(&chore, entities.InTest, 1, "assignee", now); err != nil {
		t.Fatal(err)
	}
	if chore.Status != entities.Completed || !chore.IsCompleted || chore.CompletedBy != "assignee" {
		t.Fatalf("single-member chore was not completed: %+v", chore)
	}
	if chore.CompletedAt != now || chore.ReviewRound != 1 {
		t.Fatalf("unexpected completion metadata: %+v", chore)
	}
}

func TestChoreWorkflowCompletesAfterRequiredApprovals(t *testing.T) {
	policy := chorePolicies.NewWorkflowPolicy()
	now := time.Now()
	chore := entities.Chore{AssignedTo: "assignee", Status: entities.InTest, ReviewRound: 1}

	firstOutcome := policy.ApplyReview(&chore, true, 1, 3, "reviewer-one", now)
	if firstOutcome.StatusChanged || chore.Status != entities.InTest {
		t.Fatalf("chore completed before all approvals: %+v", chore)
	}
	finalOutcome := policy.ApplyReview(&chore, true, 2, 3, "reviewer-two", now)
	if !finalOutcome.StatusChanged || finalOutcome.HistoryStatus != entities.Completed {
		t.Fatalf("unexpected review outcome: %+v", finalOutcome)
	}
	if chore.Status != entities.Completed || !chore.IsCompleted || chore.CompletedBy != "assignee" {
		t.Fatalf("approved chore was not completed: %+v", chore)
	}
}

func TestChoreWorkflowSystemCompletesAfterFinalRejectedRound(t *testing.T) {
	policy := chorePolicies.NewWorkflowPolicy()
	now := time.Now()
	chore := entities.Chore{AssignedTo: "assignee", Status: entities.InTest, ReviewRound: 3}

	outcome := policy.ApplyReview(&chore, false, 0, 3, "reviewer", now)
	if !outcome.StatusChanged || outcome.HistoryUpdater != "system" {
		t.Fatalf("unexpected rejection outcome: %+v", outcome)
	}
	if chore.Status != entities.Completed || !chore.IsCompleted || chore.CompletedBy != "system" {
		t.Fatalf("final rejected chore was not system-completed: %+v", chore)
	}
}
