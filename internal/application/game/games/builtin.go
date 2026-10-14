// Package games assembles the game modules this process can run. It is the
// only place outside a game's own package that names concrete games. Whether
// a game accepts new sessions is decided by its Game document, not here.
package games

import (
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/application/game/module"
)

// Options.TickGames registers realtime games; they need the Redis coordinator.
type Options struct{ TickGames bool }

func NewRegistry(options Options) (*module.Registry, error) {
	var ticks []module.TickModule
	if options.TickGames {
		ticks = append(ticks, houseRockets.Module())
	}
	return module.NewRegistry(ticks...)
}
