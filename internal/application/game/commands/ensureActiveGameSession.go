package commands

import (
	"context"
	"errors"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"github.com/google/uuid"
)

const EnsureActiveGameSessionCommandType = "gameSession.ensureActive"

type EnsureActiveGameSessionCommand struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	CommandID string
	UserID    string
	HouseID   string
	GameKey   string
}

type EnsureActiveGameSessionHandler struct {
	repository       gameAbstract.GameSessionRepository
	catalog          gameAbstract.GameCatalog
	membershipPolicy *housePolicies.MembershipPolicy
}

func NewEnsureActiveGameSessionHandler(
	repository gameAbstract.GameSessionRepository,
	catalog gameAbstract.GameCatalog,
	membershipPolicy *housePolicies.MembershipPolicy,
) *EnsureActiveGameSessionHandler {
	return &EnsureActiveGameSessionHandler{
		repository:       repository,
		catalog:          catalog,
		membershipPolicy: membershipPolicy,
	}
}

func (handler *EnsureActiveGameSessionHandler) Handle(
	ctx context.Context,
	command EnsureActiveGameSessionCommand,
) (gameDomain.SessionSnapshot, error) {
	descriptor, err := describeCommand(
		command.CommandID,
		command.UserID,
		EnsureActiveGameSessionCommandType,
		struct {
			HouseID string
			GameKey string
		}{command.HouseID, command.GameKey},
	)
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}

	house, err := handler.membershipPolicy.RequireMember(ctx, command.HouseID, command.UserID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	definition, err := handler.catalog.Find(command.GameKey)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	rules := definition.Rules
	if rules.MaximumPlayers > house.MaxMemberCount {
		rules.MaximumPlayers = house.MaxMemberCount
	}
	if rules.MaximumPlayers < rules.MinimumPlayers {
		return gameDomain.SessionSnapshot{}, helpers.NewConflictError("game.error.house_capacity_too_small")
	}

	if snapshot, found, err := processedSnapshot(ctx, handler.repository, descriptor); err != nil || found {
		return snapshot, err
	}
	active, err := handler.repository.FindActive(ctx, command.HouseID, command.GameKey)
	if err == nil {
		return active.Snapshot(), nil
	}
	if !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}

	now := time.Now().UTC()
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{
		SessionID:       uuid.NewString(),
		HouseID:         command.HouseID,
		GameKey:         definition.GameKey,
		ProtocolVersion: definition.ProtocolVersion,
		Mode:            definition.Mode,
		Rules:           rules,
		CreatedBy:       command.UserID,
		CreatedAt:       now,
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
	if errors.Is(err, gameAbstract.ErrActiveGameSessionExists) {
		active, findErr := handler.repository.FindActive(ctx, command.HouseID, command.GameKey)
		if findErr != nil {
			return gameDomain.SessionSnapshot{}, gameApplication.MapError(findErr)
		}
		return active.Snapshot(), nil
	}
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

var _ cqrs.CommandHandler[EnsureActiveGameSessionCommand, gameDomain.SessionSnapshot] = (*EnsureActiveGameSessionHandler)(nil)
