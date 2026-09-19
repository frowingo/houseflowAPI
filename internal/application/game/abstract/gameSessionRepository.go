package abstract

import (
	"context"
	"errors"

	gameDomain "houseflowApi/internal/application/game/domain"
)

var (
	ErrGameSessionNotFound      = errors.New("game session was not found")
	ErrGameSessionAlreadyExists = errors.New("game session already exists")
	ErrConcurrentSessionUpdate  = errors.New("game session was changed by another owner")
	ErrInvalidPersistenceBatch  = errors.New("game session snapshot and event batch are inconsistent")
	ErrCommandIDRequired        = errors.New("game session command ID is required")
	ErrCommandActorRequired     = errors.New("game session command actor is required")
	ErrCommandTypeRequired      = errors.New("game session command type is required")
	ErrCommandPayloadRequired   = errors.New("game session command payload hash is required")
	ErrCommandIDReused          = errors.New("game session command ID was reused with different content")
)

type CommandDescriptor struct {
	CommandID   string
	ActorID     string
	CommandType string
	PayloadHash string
}

type PersistenceResult struct {
	SessionID        string
	AlreadyProcessed bool
}

type GameSessionRepository interface {
	Create(
		ctx context.Context,
		snapshot gameDomain.SessionSnapshot,
		events []gameDomain.SessionEvent,
		command CommandDescriptor,
	) (PersistenceResult, error)
	FindByID(ctx context.Context, sessionID string) (*gameDomain.GameSession, error)
	FindProcessedCommand(ctx context.Context, command CommandDescriptor) (PersistenceResult, bool, error)
	Save(
		ctx context.Context,
		expectedVersion int64,
		snapshot gameDomain.SessionSnapshot,
		events []gameDomain.SessionEvent,
		command CommandDescriptor,
	) (PersistenceResult, error)
}
