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

const LeaveGameSessionCommandType = "gameSession.leave"

type LeaveGameSessionCommand struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	CommandID string
	SessionID string
	UserID    string
}

type LeaveGameSessionHandler struct {
	repository       gameAbstract.GameSessionRepository
	membershipPolicy *housePolicies.MembershipPolicy
}

func NewLeaveGameSessionHandler(
	repository gameAbstract.GameSessionRepository,
	membershipPolicy *housePolicies.MembershipPolicy,
) *LeaveGameSessionHandler {
	return &LeaveGameSessionHandler{repository: repository, membershipPolicy: membershipPolicy}
}

func (handler *LeaveGameSessionHandler) Handle(
	ctx context.Context,
	command LeaveGameSessionCommand,
) (gameDomain.SessionSnapshot, error) {
	descriptor, err := describeCommand(command.CommandID, command.UserID, LeaveGameSessionCommandType, struct {
		SessionID string
	}{command.SessionID})
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
	if err := session.LeavePlayer(command.UserID, time.Now().UTC()); err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	return saveSession(ctx, handler.repository, session, expectedVersion, descriptor)
}

var _ cqrs.CommandHandler[LeaveGameSessionCommand, gameDomain.SessionSnapshot] = (*LeaveGameSessionHandler)(nil)
