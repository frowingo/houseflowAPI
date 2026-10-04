package queries

import (
	"context"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/infrastructure/cqrs"
)

type GetGameSessionQuery struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	SessionID string
	UserID    string
}

type GetGameSessionHandler struct {
	repository       gameAbstract.GameSessionRepository
	membershipPolicy *housePolicies.MembershipPolicy
}

func NewGetGameSessionHandler(
	repository gameAbstract.GameSessionRepository,
	membershipPolicy *housePolicies.MembershipPolicy,
) *GetGameSessionHandler {
	return &GetGameSessionHandler{repository: repository, membershipPolicy: membershipPolicy}
}

func (handler *GetGameSessionHandler) Handle(
	ctx context.Context,
	query GetGameSessionQuery,
) (gameDomain.SessionSnapshot, error) {
	session, err := handler.repository.FindByID(ctx, query.SessionID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	snapshot := session.Snapshot()
	if _, err := handler.membershipPolicy.RequireMember(ctx, snapshot.HouseID, query.UserID); err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	return snapshot, nil
}

var _ cqrs.QueryHandler[GetGameSessionQuery, gameDomain.SessionSnapshot] = (*GetGameSessionHandler)(nil)
