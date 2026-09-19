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

const JoinGameSessionCommandType = "gameSession.join"

type JoinGameSessionCommand struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	CommandID string
	SessionID string
	UserID    string
}

type JoinGameSessionHandler struct {
	repository       gameAbstract.GameSessionRepository
	membershipPolicy *housePolicies.MembershipPolicy
}

func NewJoinGameSessionHandler(
	repository gameAbstract.GameSessionRepository,
	membershipPolicy *housePolicies.MembershipPolicy,
) *JoinGameSessionHandler {
	return &JoinGameSessionHandler{repository: repository, membershipPolicy: membershipPolicy}
}

func (handler *JoinGameSessionHandler) Handle(
	ctx context.Context,
	command JoinGameSessionCommand,
) (gameDomain.SessionSnapshot, error) {
	descriptor, err := describeCommand(command.CommandID, command.UserID, JoinGameSessionCommandType, struct {
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
	if err := session.JoinPlayer(command.UserID, time.Now().UTC()); err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	return saveSession(ctx, handler.repository, session, expectedVersion, descriptor)
}

var _ cqrs.CommandHandler[JoinGameSessionCommand, gameDomain.SessionSnapshot] = (*JoinGameSessionHandler)(nil)
