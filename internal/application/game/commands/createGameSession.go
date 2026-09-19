package commands

import (
	"context"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"github.com/google/uuid"
)

const createGameSessionCommandType = "gameSession.create"

type CreateGameSessionCommand struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	CommandID       string
	UserID          string
	HouseID         string
	GameKey         string
	ProtocolVersion int
	Mode            gameDomain.GameMode
	Rules           gameDomain.SessionRules
}

type CreateGameSessionHandler struct {
	repository       gameAbstract.GameSessionRepository
	membershipPolicy *housePolicies.MembershipPolicy
}

func NewCreateGameSessionHandler(
	repository gameAbstract.GameSessionRepository,
	membershipPolicy *housePolicies.MembershipPolicy,
) *CreateGameSessionHandler {
	return &CreateGameSessionHandler{
		repository:       repository,
		membershipPolicy: membershipPolicy,
	}
}

func (handler *CreateGameSessionHandler) Handle(
	ctx context.Context,
	command CreateGameSessionCommand,
) (gameDomain.SessionSnapshot, error) {
	descriptor, err := describeCommand(command.CommandID, command.UserID, createGameSessionCommandType, struct {
		HouseID         string
		GameKey         string
		ProtocolVersion int
		Mode            gameDomain.GameMode
		Rules           gameDomain.SessionRules
	}{command.HouseID, command.GameKey, command.ProtocolVersion, command.Mode, command.Rules})
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}

	house, err := handler.membershipPolicy.RequireMember(ctx, command.HouseID, command.UserID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	if command.Rules.MaximumPlayers > house.MaxMemberCount {
		return gameDomain.SessionSnapshot{}, helpers.NewLocalizedError("game.error.max_players_exceeds_house")
	}
	if snapshot, found, err := processedSnapshot(ctx, handler.repository, descriptor); err != nil || found {
		return snapshot, err
	}

	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{
		SessionID:       uuid.NewString(),
		HouseID:         command.HouseID,
		GameKey:         command.GameKey,
		ProtocolVersion: command.ProtocolVersion,
		Mode:            command.Mode,
		Rules:           command.Rules,
		CreatedBy:       command.UserID,
		CreatedAt:       time.Now().UTC(),
	})
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	result, err := handler.repository.Create(
		ctx,
		session.Snapshot(),
		session.PendingEvents(),
		descriptor,
	)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	if result.AlreadyProcessed {
		stored, err := handler.repository.FindByID(ctx, result.SessionID)
		if err != nil {
			return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
		}
		return stored.Snapshot(), nil
	}
	session.ClearPendingEvents()
	return session.Snapshot(), nil
}

var _ cqrs.CommandHandler[CreateGameSessionCommand, gameDomain.SessionSnapshot] = (*CreateGameSessionHandler)(nil)
