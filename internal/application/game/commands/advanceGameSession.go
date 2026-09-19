package commands

import (
	"context"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/infrastructure/cqrs"
)

const (
	AdvanceGameSessionCommandType = "gameSession.advance"
	GameSessionRuntimeActorID     = "gameSessionRuntime"
)

type AdvanceGameSessionCommand struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	CommandID string
	SessionID string
	AdvanceAt time.Time
}

type AdvanceGameSessionHandler struct {
	repository gameAbstract.GameSessionRepository
}

func NewAdvanceGameSessionHandler(repository gameAbstract.GameSessionRepository) *AdvanceGameSessionHandler {
	return &AdvanceGameSessionHandler{repository: repository}
}

func (handler *AdvanceGameSessionHandler) Handle(
	ctx context.Context,
	command AdvanceGameSessionCommand,
) (gameDomain.SessionSnapshot, error) {
	descriptor, err := describeCommand(
		command.CommandID,
		GameSessionRuntimeActorID,
		AdvanceGameSessionCommandType,
		struct {
			SessionID string
			AdvanceAt time.Time
		}{command.SessionID, command.AdvanceAt.UTC()},
	)
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	session, err := handler.repository.FindByID(ctx, command.SessionID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	if snapshot, found, err := processedSnapshot(ctx, handler.repository, descriptor); err != nil || found {
		return snapshot, err
	}

	expectedVersion := session.Version()
	if err := session.Advance(command.AdvanceAt.UTC()); err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	return saveSession(ctx, handler.repository, session, expectedVersion, descriptor)
}

var _ cqrs.CommandHandler[AdvanceGameSessionCommand, gameDomain.SessionSnapshot] = (*AdvanceGameSessionHandler)(nil)
