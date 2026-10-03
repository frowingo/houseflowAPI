package queries

import (
	"context"
	"errors"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

type GetMatchResultQuery struct {
	cqrs.Request[houseRockets.HouseRocketsResultModel]
	SessionID string
	UserID    string
}
type GetMatchResultHandler struct {
	sessions   gameAbstract.GameSessionRepository
	matches    gameAbstract.GameMatchRepository
	membership *housePolicies.MembershipPolicy
}

func NewGetMatchResultHandler(sessions gameAbstract.GameSessionRepository, matches gameAbstract.GameMatchRepository, membership *housePolicies.MembershipPolicy) *GetMatchResultHandler {
	return &GetMatchResultHandler{sessions, matches, membership}
}
func (handler *GetMatchResultHandler) Handle(ctx context.Context, query GetMatchResultQuery) (houseRockets.HouseRocketsResultModel, error) {
	session, err := handler.sessions.FindByID(ctx, query.SessionID)
	if err != nil {
		return houseRockets.HouseRocketsResultModel{}, gameApplication.MapError(err)
	}
	if _, err := handler.membership.RequireMember(ctx, session.Snapshot().HouseID, query.UserID); err != nil {
		return houseRockets.HouseRocketsResultModel{}, err
	}
	result, err := handler.matches.FindResult(ctx, query.SessionID)
	if errors.Is(err, gameAbstract.ErrMatchResultNotFound) {
		return houseRockets.HouseRocketsResultModel{}, helpers.NewNotFoundError("houseRockets.error.result_not_found")
	}
	if err != nil {
		return houseRockets.HouseRocketsResultModel{}, err
	}
	return houseRockets.DecodeMatchResult(result)
}
