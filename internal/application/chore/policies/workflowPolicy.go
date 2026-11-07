package policies

import (
	"time"

	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
)

const (
	maxReviewRounds = 3
	systemUpdater   = "system"
)

type ReviewOutcome struct {
	StatusChanged  bool
	HistoryStatus  entities.ChoreStatus
	HistoryUpdater string
}

// WorkflowPolicy owns chore status-transition and review-completion rules.
type WorkflowPolicy struct{}

func NewWorkflowPolicy() *WorkflowPolicy {
	return &WorkflowPolicy{}
}

func (p *WorkflowPolicy) Advance(
	chore *entities.Chore,
	targetStatus entities.ChoreStatus,
	houseMemberCount int,
	userID string,
	now time.Time,
) error {
	if chore.AssignedTo != userID {
		return helpers.NewLocalizedError("chore.error.only_assignee_can_advance")
	}

	nextStatus, ok := nextStatus(chore.Status)
	if !ok || targetStatus != nextStatus {
		return helpers.NewLocalizedError("chore.error.invalid_status_transition")
	}

	chore.Status = targetStatus
	chore.IsCompleted = false
	chore.CompletedBy = ""
	chore.CompletedAt = time.Time{}
	if targetStatus != entities.InTest {
		return nil
	}
	if chore.ReviewRound >= maxReviewRounds {
		return helpers.NewLocalizedError("chore.error.max_review_round_reached")
	}

	chore.ReviewRound++
	if houseMemberCount <= 1 {
		chore.Status = entities.Completed
		chore.IsCompleted = true
		chore.CompletedBy = chore.AssignedTo
		chore.CompletedAt = now
	}
	return nil
}

func (p *WorkflowPolicy) ApplyReview(
	chore *entities.Chore,
	isApproved bool,
	approvedCount int64,
	houseMemberCount int,
	reviewerID string,
	now time.Time,
) ReviewOutcome {
	if !isApproved {
		chore.Status = entities.Progress
		chore.IsCompleted = false
		chore.CompletedBy = ""
		chore.CompletedAt = time.Time{}
		updater := reviewerID
		if chore.ReviewRound >= maxReviewRounds {
			markFailed(chore)
			updater = systemUpdater
		}
		return ReviewOutcome{
			StatusChanged:  true,
			HistoryStatus:  chore.Status,
			HistoryUpdater: updater,
		}
	}

	if approvedCount < int64(houseMemberCount-1) {
		return ReviewOutcome{}
	}

	chore.Status = entities.Completed
	chore.IsCompleted = true
	chore.CompletedBy = chore.AssignedTo
	chore.CompletedAt = now
	return ReviewOutcome{
		StatusChanged:  true,
		HistoryStatus:  entities.Completed,
		HistoryUpdater: reviewerID,
	}
}

// ApplyDeadline marks a chore as failed once its due date is reached. A chore
// completed strictly before its due date remains completed permanently.
func (p *WorkflowPolicy) ApplyDeadline(chore *entities.Chore, now time.Time) bool {
	if chore.Status == entities.Failed || chore.DueDate.IsZero() || now.Before(chore.DueDate) {
		return false
	}
	if chore.Status == entities.Completed && !chore.CompletedAt.IsZero() && chore.CompletedAt.Before(chore.DueDate) {
		return false
	}
	markFailed(chore)
	return true
}

func markFailed(chore *entities.Chore) {
	chore.Status = entities.Failed
	chore.IsCompleted = false
	chore.CompletedBy = ""
	chore.CompletedAt = time.Time{}
}

func nextStatus(current entities.ChoreStatus) (entities.ChoreStatus, bool) {
	switch current {
	case entities.Draft:
		return entities.Progress, true
	case entities.Progress:
		return entities.InTest, true
	default:
		return current, false
	}
}
