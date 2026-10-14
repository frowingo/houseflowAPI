package queries

import (
	"context"

	gameApplication "houseflowApi/internal/application/game"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/infrastructure/cqrs"
)

// GetGameCatalogQuery lists the games a house can start now, with the rules
// and client settings a new session would get.
type GetGameCatalogQuery struct {
	cqrs.Request[[]gameApplication.CatalogEntry]
	HouseID string
	UserID  string
}

type GetGameCatalogHandler struct {
	catalog          *gameApplication.Catalog
	membershipPolicy *housePolicies.MembershipPolicy
}

func NewGetGameCatalogHandler(catalog *gameApplication.Catalog, membershipPolicy *housePolicies.MembershipPolicy) *GetGameCatalogHandler {
	return &GetGameCatalogHandler{catalog: catalog, membershipPolicy: membershipPolicy}
}

func (handler *GetGameCatalogHandler) Handle(ctx context.Context, query GetGameCatalogQuery) ([]gameApplication.CatalogEntry, error) {
	house, err := handler.membershipPolicy.RequireMember(ctx, query.HouseID, query.UserID)
	if err != nil {
		return nil, err
	}
	available := []gameApplication.CatalogEntry{}
	for _, entry := range handler.catalog.Entries() {
		if !entry.Config.AvailableTo(query.HouseID) {
			continue
		}
		// Sessions are capped by the house the same way when they are created.
		entry.Config.Rules.MaximumPlayers = min(entry.Config.Rules.MaximumPlayers, house.MaxMemberCount)
		available = append(available, entry)
	}
	return available, nil
}

// GetGameConfigsQuery lists every registered game with its current config,
// including switched-off games, for operators.
type GetGameConfigsQuery struct {
	cqrs.Request[[]gameApplication.CatalogEntry]
}

type GetGameConfigsHandler struct {
	catalog *gameApplication.Catalog
}

func NewGetGameConfigsHandler(catalog *gameApplication.Catalog) *GetGameConfigsHandler {
	return &GetGameConfigsHandler{catalog: catalog}
}

func (handler *GetGameConfigsHandler) Handle(_ context.Context, _ GetGameConfigsQuery) ([]gameApplication.CatalogEntry, error) {
	return handler.catalog.Entries(), nil
}

var (
	_ cqrs.QueryHandler[GetGameCatalogQuery, []gameApplication.CatalogEntry] = (*GetGameCatalogHandler)(nil)
	_ cqrs.QueryHandler[GetGameConfigsQuery, []gameApplication.CatalogEntry] = (*GetGameConfigsHandler)(nil)
)
