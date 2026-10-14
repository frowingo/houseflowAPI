package queries

import (
	"context"
	"errors"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	"houseflowApi/internal/application/game/module"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

// GetMatchResultQuery returns the committed result in its game's wire model.
type GetMatchResultQuery struct {
	cqrs.Request[any]
	SessionID string
	UserID    string
}

type GetMatchResultHandler struct {
	sessions   gameAbstract.GameSessionRepository
	matches    gameAbstract.GameMatchRepository
	games      *module.Registry
	membership *housePolicies.MembershipPolicy
}

func NewGetMatchResultHandler(sessions gameAbstract.GameSessionRepository, matches gameAbstract.GameMatchRepository, games *module.Registry, membership *housePolicies.MembershipPolicy) *GetMatchResultHandler {
	return &GetMatchResultHandler{sessions: sessions, matches: matches, games: games, membership: membership}
}

// The immutable result carries its house, so authorization does not depend on
// the session document, which may already have expired.
func (handler *GetMatchResultHandler) Handle(ctx context.Context, query GetMatchResultQuery) (any, error) {
	result, err := handler.matches.FindResult(ctx, query.SessionID)
	if errors.Is(err, gameAbstract.ErrMatchResultNotFound) {
		return nil, handler.missingResult(ctx, query)
	}
	if err != nil {
		return nil, err
	}
	if _, err := handler.membership.RequireMember(ctx, result.HouseID, query.UserID); err != nil {
		return nil, err
	}
	codec, exists := handler.games.Results(result.GameKey)
	if !exists {
		return nil, gameApplication.MapError(gameAbstract.ErrGameDefinitionNotFound)
	}
	return codec.DecodeResult(result)
}

// missingResult keeps the session-not-found/forbidden/result-not-found order so
// non-members cannot probe which sessions exist.
func (handler *GetMatchResultHandler) missingResult(ctx context.Context, query GetMatchResultQuery) error {
	session, err := handler.sessions.FindByID(ctx, query.SessionID)
	if err != nil {
		return gameApplication.MapError(err)
	}
	snapshot := session.Snapshot()
	if _, err := handler.membership.RequireMember(ctx, snapshot.HouseID, query.UserID); err != nil {
		return err
	}
	return helpers.NewNotFoundError(module.ErrorCode(snapshot.GameKey, "result_not_found"))
}

var _ cqrs.QueryHandler[GetMatchResultQuery, any] = (*GetMatchResultHandler)(nil)
