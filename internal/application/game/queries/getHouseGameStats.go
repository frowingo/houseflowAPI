package queries

import (
	"context"
	"sort"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	"houseflowApi/internal/application/game/module"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/infrastructure/cqrs"
)

// GetHouseGameStatsQuery counts, per game, how many completed matches each
// current member of the house played and won.
type GetHouseGameStatsQuery struct {
	cqrs.Request[[]GameStats]
	HouseID string
	UserID  string
}

// GameStats lists every current member, including those who never played the
// game, ordered by wins, then matches played, then house member order.
type GameStats struct {
	GameKey string
	Players []PlayerGameStats
}

type PlayerGameStats struct {
	UserID        string
	Wins          int
	MatchesPlayed int
}

type GetHouseGameStatsHandler struct {
	matches    gameAbstract.GameMatchRepository
	games      *module.Registry
	membership *housePolicies.MembershipPolicy
}

func NewGetHouseGameStatsHandler(matches gameAbstract.GameMatchRepository, games *module.Registry, membership *housePolicies.MembershipPolicy) *GetHouseGameStatsHandler {
	return &GetHouseGameStatsHandler{matches: matches, games: games, membership: membership}
}

// Handle lists only games a current member has completed a match of. Matches
// of former members still count for the members who played them; results of
// games this deployment no longer runs are skipped.
func (handler *GetHouseGameStatsHandler) Handle(ctx context.Context, query GetHouseGameStatsQuery) ([]GameStats, error) {
	house, err := handler.membership.RequireMember(ctx, query.HouseID, query.UserID)
	if err != nil {
		return nil, err
	}
	results, err := handler.matches.CompletedResults(ctx, query.HouseID)
	if err != nil {
		return nil, err
	}
	members := make(map[string]int, len(house.MemberIds))
	for index, memberID := range house.MemberIds {
		members[memberID] = index
	}
	byGame := map[string][]PlayerGameStats{}
	for _, result := range results {
		codec, exists := handler.games.Results(result.GameKey)
		if !exists {
			continue
		}
		standing, err := codec.Standing(result)
		if err != nil {
			return nil, gameApplication.MapError(err)
		}
		for _, playerID := range standing.PlayerIDs {
			index, member := members[playerID]
			if !member {
				continue
			}
			players, exists := byGame[result.GameKey]
			if !exists {
				players = make([]PlayerGameStats, len(house.MemberIds))
				for memberIndex, memberID := range house.MemberIds {
					players[memberIndex].UserID = memberID
				}
				byGame[result.GameKey] = players
			}
			players[index].MatchesPlayed++
			if playerID == standing.WinnerID {
				players[index].Wins++
			}
		}
	}
	stats := make([]GameStats, 0, len(byGame))
	for gameKey, players := range byGame {
		sort.SliceStable(players, func(i, j int) bool {
			if players[i].Wins != players[j].Wins {
				return players[i].Wins > players[j].Wins
			}
			return players[i].MatchesPlayed > players[j].MatchesPlayed
		})
		stats = append(stats, GameStats{GameKey: gameKey, Players: players})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].GameKey < stats[j].GameKey })
	return stats, nil
}

var _ cqrs.QueryHandler[GetHouseGameStatsQuery, []GameStats] = (*GetHouseGameStatsHandler)(nil)
