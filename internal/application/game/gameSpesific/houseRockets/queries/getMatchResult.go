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

// The immutable result carries its house, so authorization does not depend on
// the session document, which may already have expired.
func (handler *GetMatchResultHandler) Handle(ctx context.Context, query GetMatchResultQuery) (houseRockets.HouseRocketsResultModel, error) {
	result, err := handler.matches.FindResult(ctx, query.SessionID)
	if errors.Is(err, gameAbstract.ErrMatchResultNotFound) {
		return houseRockets.HouseRocketsResultModel{}, handler.missingResult(ctx, query)
	}
	if err != nil {
		return houseRockets.HouseRocketsResultModel{}, err
	}
	if _, err := handler.membership.RequireMember(ctx, result.HouseID, query.UserID); err != nil {
		return houseRockets.HouseRocketsResultModel{}, err
	}
	return houseRockets.DecodeMatchResult(result)
}

// missingResult keeps the session-not-found/forbidden/result-not-found order so
// non-members cannot probe which sessions exist.
func (handler *GetMatchResultHandler) missingResult(ctx context.Context, query GetMatchResultQuery) error {
	session, err := handler.sessions.FindByID(ctx, query.SessionID)
	if err != nil {
		return gameApplication.MapError(err)
	}
	if _, err := handler.membership.RequireMember(ctx, session.Snapshot().HouseID, query.UserID); err != nil {
		return err
	}
	return helpers.NewNotFoundError("houseRockets.error.result_not_found")
}
