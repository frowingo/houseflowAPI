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
	GameSessionRuntimeActorID     = gameAbstract.SystemActorID
)

type AdvanceGameSessionCommand struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	CommandID string
	SessionID string
	AdvanceAt time.Time
}

type AdvanceGameSessionHandler struct {
	repository gameAbstract.GameSessionRepository
	turns      *TurnEngine
}

func NewAdvanceGameSessionHandler(repository gameAbstract.GameSessionRepository, turns *TurnEngine) *AdvanceGameSessionHandler {
	return &AdvanceGameSessionHandler{repository: repository, turns: turns}
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
	advanceAt := command.AdvanceAt.UTC()
	// A running session has no lobby deadline left; its deadline is a turn
	// game's, which the engine resolves at the turn's own time.
	if session.State() != gameDomain.SessionRunning {
		if err := session.Advance(advanceAt); err != nil {
			return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
		}
	}
	result, err := handler.turns.settle(session, advanceAt)
	if err != nil {
		return gameDomain.SessionSnapshot{}, mapTurnError(err)
	}
	return handler.turns.commit(ctx, handler.repository, session, expectedVersion, descriptor, result)
}

var _ cqrs.CommandHandler[AdvanceGameSessionCommand, gameDomain.SessionSnapshot] = (*AdvanceGameSessionHandler)(nil)
