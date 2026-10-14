// Package games assembles the game modules this process can run. It is the
// only place outside a game's own package that names concrete games. Whether
// a game accepts new sessions is decided by its Game document, not here.
package games

import (
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	ticTacToe "houseflowApi/internal/application/game/games/ticTacToe"
	"houseflowApi/internal/application/game/module"
)

// Options.Realtime registers the games played over the realtime gateway, of
// every lane; they need the Redis coordinator.
type Options struct{ Realtime bool }

func NewRegistry(options Options) (*module.Registry, error) {
	var games []module.Game
	if options.Realtime {
		games = append(games, houseRockets.Module(), ticTacToe.Module())
	}
	return module.NewRegistry(games...)
}
