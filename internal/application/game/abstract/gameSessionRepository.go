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
)

type GameSessionRepository interface {
	Create(ctx context.Context,snapshot gameDomain.SessionSnapshot,events []gameDomain.SessionEvent,) error
	FindByID(ctx context.Context, sessionID string) (*gameDomain.GameSession, error)
	Save(ctx context.Context,expectedVersion int64,snapshot gameDomain.SessionSnapshot,events []gameDomain.SessionEvent,) error
}
