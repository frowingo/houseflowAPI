package commands

import (
	"context"
	"encoding/json"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/infrastructure/cqrs"
)

// ApplyTurnMoveCommand is one player's move in a turn-based game. It is a
// durable command: the new state commits with its receipt, so a retried move
// is applied once. MoveType is the client message type the module decodes.
type ApplyTurnMoveCommand struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	CommandID string
	SessionID string
	UserID    string
	MoveType  string
	Payload   json.RawMessage
}

type ApplyTurnMoveHandler struct {
	repository       gameAbstract.GameSessionRepository
	membershipPolicy *housePolicies.MembershipPolicy
	turns            *TurnEngine
}

func NewApplyTurnMoveHandler(repository gameAbstract.GameSessionRepository, membershipPolicy *housePolicies.MembershipPolicy, turns *TurnEngine) *ApplyTurnMoveHandler {
	return &ApplyTurnMoveHandler{repository: repository, membershipPolicy: membershipPolicy, turns: turns}
}

func (handler *ApplyTurnMoveHandler) Handle(ctx context.Context, command ApplyTurnMoveCommand) (gameDomain.SessionSnapshot, error) {
	descriptor, err := describeCommand(command.CommandID, command.UserID, command.MoveType, struct {
		SessionID string
		Payload   json.RawMessage
	}{command.SessionID, command.Payload})
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	session, err := handler.repository.FindByID(ctx, command.SessionID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	// Only a turn game owns move messages; anything else is not a command.
	if _, exists := handler.turns.game(session.Snapshot().GameKey); !exists {
		return gameDomain.SessionSnapshot{}, module.ErrUnsupportedIntent
	}
	if _, err := handler.membershipPolicy.RequireMember(ctx, session.Snapshot().HouseID, command.UserID); err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	if snapshot, found, err := processedSnapshot(ctx, handler.repository, descriptor); err != nil || found {
		return snapshot, err
	}
	expectedVersion := session.Version()
	result, err := handler.turns.move(session, command.UserID, command.MoveType, command.Payload, time.Now().UTC())
	if err != nil {
		return gameDomain.SessionSnapshot{}, mapTurnError(err)
	}
	return handler.turns.commit(ctx, handler.repository, session, expectedVersion, descriptor, result)
}

var _ cqrs.CommandHandler[ApplyTurnMoveCommand, gameDomain.SessionSnapshot] = (*ApplyTurnMoveHandler)(nil)
