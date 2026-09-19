package commands

import (
	"context"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/infrastructure/cqrs"
)

const setPlayerReadyCommandType = "gameSession.setReady"

type SetPlayerReadyCommand struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	CommandID string
	SessionID string
	UserID    string
	Ready     bool
}

type SetPlayerReadyHandler struct {
	repository       gameAbstract.GameSessionRepository
	membershipPolicy *housePolicies.MembershipPolicy
}

func NewSetPlayerReadyHandler(
	repository gameAbstract.GameSessionRepository,
	membershipPolicy *housePolicies.MembershipPolicy,
) *SetPlayerReadyHandler {
	return &SetPlayerReadyHandler{repository: repository, membershipPolicy: membershipPolicy}
}

func (handler *SetPlayerReadyHandler) Handle(
	ctx context.Context,
	command SetPlayerReadyCommand,
) (gameDomain.SessionSnapshot, error) {
	descriptor, err := describeCommand(command.CommandID, command.UserID, setPlayerReadyCommandType, struct {
		SessionID string
		Ready     bool
	}{command.SessionID, command.Ready})
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	session, err := handler.repository.FindByID(ctx, command.SessionID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	if _, err := handler.membershipPolicy.RequireMember(ctx, session.Snapshot().HouseID, command.UserID); err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	if snapshot, found, err := processedSnapshot(ctx, handler.repository, descriptor); err != nil || found {
		return snapshot, err
	}

	expectedVersion := session.Version()
	if err := session.SetReady(command.UserID, command.Ready, time.Now().UTC()); err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	return saveSession(ctx, handler.repository, session, expectedVersion, descriptor)
}

var _ cqrs.CommandHandler[SetPlayerReadyCommand, gameDomain.SessionSnapshot] = (*SetPlayerReadyHandler)(nil)
