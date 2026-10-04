package commands

import (
	"context"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/infrastructure/cqrs"
)

// System-only command: it is intentionally not exposed by a websocket decoder.
type ReconcileGameSessionPlayersCommand struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	CommandID string
	SessionID string
	PlayerIDs []string
	Reason    string
	At        time.Time
}

type ReconcileGameSessionPlayersHandler struct {
	repository gameAbstract.GameSessionRepository
}

func NewReconcileGameSessionPlayersHandler(repository gameAbstract.GameSessionRepository) *ReconcileGameSessionPlayersHandler {
	return &ReconcileGameSessionPlayersHandler{repository: repository}
}
func (handler *ReconcileGameSessionPlayersHandler) Handle(ctx context.Context, command ReconcileGameSessionPlayersCommand) (gameDomain.SessionSnapshot, error) {
	descriptor, err := describeCommand(command.CommandID, GameSessionRuntimeActorID, "gameSession.reconcilePlayers", struct {
		SessionID string
		PlayerIDs []string
		Reason    string
		At        time.Time
	}{command.SessionID, command.PlayerIDs, command.Reason, command.At.UTC()})
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	if snapshot, found, err := processedSnapshot(ctx, handler.repository, descriptor); found || err != nil {
		return snapshot, err
	}
	session, err := handler.repository.FindByID(ctx, command.SessionID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	version := session.Version()
	if command.At.Before(session.Snapshot().UpdatedAt) {
		command.At = session.Snapshot().UpdatedAt
	}
	if err := session.ExcludePlayers(command.PlayerIDs, command.Reason, command.At); err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	return saveSession(ctx, handler.repository, session, version, descriptor)
}
