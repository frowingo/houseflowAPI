package queries

import (
	"context"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/infrastructure/cqrs"
)

type GetActiveGameSessionQuery struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	HouseID string
	GameKey string
	UserID  string
}

type GetActiveGameSessionHandler struct {
	repository       gameAbstract.GameSessionRepository
	catalog          gameAbstract.GameCatalog
	membershipPolicy *housePolicies.MembershipPolicy
}

func NewGetActiveGameSessionHandler(
	repository gameAbstract.GameSessionRepository,
	catalog gameAbstract.GameCatalog,
	membershipPolicy *housePolicies.MembershipPolicy,
) *GetActiveGameSessionHandler {
	return &GetActiveGameSessionHandler{
		repository:       repository,
		catalog:          catalog,
		membershipPolicy: membershipPolicy,
	}
}

func (handler *GetActiveGameSessionHandler) Handle(
	ctx context.Context,
	query GetActiveGameSessionQuery,
) (gameDomain.SessionSnapshot, error) {
	if _, err := handler.membershipPolicy.RequireMember(ctx, query.HouseID, query.UserID); err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	if _, err := handler.catalog.Find(query.GameKey); err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	session, err := handler.repository.FindActive(ctx, query.HouseID, query.GameKey)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapActiveSessionError(err)
	}
	return session.Snapshot(), nil
}

var _ cqrs.QueryHandler[GetActiveGameSessionQuery, gameDomain.SessionSnapshot] = (*GetActiveGameSessionHandler)(nil)
