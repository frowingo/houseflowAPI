package commands

import (
	"context"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

const CancelGameSessionCommandType = "gameSession.cancel"

type CancelGameSessionCommand struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	CommandID string
	SessionID string
	UserID    string
}

type CancelGameSessionHandler struct {
	repository       gameAbstract.GameSessionRepository
	membershipPolicy *housePolicies.MembershipPolicy
	games            *module.Registry
}

func NewCancelGameSessionHandler(
	repository gameAbstract.GameSessionRepository,
	membershipPolicy *housePolicies.MembershipPolicy,
	games *module.Registry,
) *CancelGameSessionHandler {
	return &CancelGameSessionHandler{repository: repository, membershipPolicy: membershipPolicy, games: games}
}

func (handler *CancelGameSessionHandler) Handle(
	ctx context.Context,
	command CancelGameSessionCommand,
) (gameDomain.SessionSnapshot, error) {
	descriptor, err := describeCommand(command.CommandID, command.UserID, CancelGameSessionCommandType, struct {
		SessionID string
	}{command.SessionID})
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	session, err := handler.repository.FindByID(ctx, command.SessionID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	snapshot := session.Snapshot()
	house, err := handler.membershipPolicy.RequireMember(ctx, snapshot.HouseID, command.UserID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	if command.UserID != snapshot.CreatedBy && command.UserID != house.OwnerId {
		return gameDomain.SessionSnapshot{}, helpers.NewForbiddenError("game.error.cancel_forbidden")
	}
	if processed, found, err := processedSnapshot(ctx, handler.repository, descriptor); err != nil || found {
		return processed, err
	}
	// A running match ends through its runtime, which owns the result.
	if snapshot.State == gameDomain.SessionRunning && handler.games.HasRuntime(snapshot.GameKey) {
		return gameDomain.SessionSnapshot{}, helpers.NewConflictError(module.ErrorCode(snapshot.GameKey, "runtime_completion_required"))
	}

	expectedVersion := session.Version()
	if err := session.Cancel(gameDomain.EndReasonCancelledByUser, time.Now().UTC()); err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	return saveSession(ctx, handler.repository, session, expectedVersion, descriptor)
}

var _ cqrs.CommandHandler[CancelGameSessionCommand, gameDomain.SessionSnapshot] = (*CancelGameSessionHandler)(nil)
