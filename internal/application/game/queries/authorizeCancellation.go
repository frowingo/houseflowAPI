package queries

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

type AuthorizeCancellationQuery struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	SessionID string
	UserID    string
	CommandID string
}
type AuthorizeCancellationHandler struct {
	sessions   gameAbstract.GameSessionRepository
	membership *housePolicies.MembershipPolicy
}

func NewAuthorizeCancellationHandler(sessions gameAbstract.GameSessionRepository, membership *housePolicies.MembershipPolicy) *AuthorizeCancellationHandler {
	return &AuthorizeCancellationHandler{sessions, membership}
}
func (handler *AuthorizeCancellationHandler) Handle(ctx context.Context, query AuthorizeCancellationQuery) (gameDomain.SessionSnapshot, error) {
	session, err := handler.sessions.FindByID(ctx, query.SessionID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	snapshot := session.Snapshot()
	house, err := handler.membership.RequireMember(ctx, snapshot.HouseID, query.UserID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	if query.UserID != snapshot.CreatedBy && query.UserID != house.OwnerId {
		return gameDomain.SessionSnapshot{}, helpers.NewForbiddenError("game.error.cancel_forbidden")
	}
	if query.CommandID == "" {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(gameAbstract.ErrCommandIDRequired)
	}
	payload, _ := json.Marshal(struct{ SessionID string }{query.SessionID})
	digest := sha256.Sum256(payload)
	descriptor := gameAbstract.CommandDescriptor{CommandID: query.CommandID, ActorID: query.UserID, CommandType: gameCommands.CancelGameSessionCommandType, PayloadHash: hex.EncodeToString(digest[:])}
	if _, _, err := handler.sessions.FindProcessedCommand(ctx, descriptor); err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	return snapshot, nil
}
