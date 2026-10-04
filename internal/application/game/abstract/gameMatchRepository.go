package abstract

import (
	"context"
	"errors"
	gameDomain "houseflowApi/internal/application/game/domain"
)

var (
	ErrMatchResultNotFound = errors.New("match result not found")
	ErrMatchResultConflict = errors.New("match already has a different result")
)

// MatchCompletion is one atomic terminal transition, including the owner fence,
// command receipts, immutable result, active slot release and outbox events.
type MatchCompletion struct {
	ExpectedVersion int64
	Session         gameDomain.SessionSnapshot
	Events          []gameDomain.SessionEvent
	Result          gameDomain.MatchResult
	Owner           RuntimeOwner
	Command         CommandDescriptor
	TriggerCommand  *CommandDescriptor
	ResultEventType string
}

type GameMatchRepository interface {
	FindResult(ctx context.Context, sessionID string) (gameDomain.MatchResult, error)
	Complete(ctx context.Context, completion MatchCompletion) (gameDomain.MatchResult, error)
	MarkPublished(ctx context.Context, sessionID string) error
}
