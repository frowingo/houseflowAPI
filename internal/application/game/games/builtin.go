// Package games assembles the game modules enabled in this process. It is the
// only place outside a game's own package that names concrete games.
package games

import (
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/application/game/module"
)

type Options struct{ EnableHouseRockets bool }

func NewRegistry(options Options) (*module.Registry, error) {
	var ticks []module.TickModule
	if options.EnableHouseRockets {
		ticks = append(ticks, houseRockets.Module())
	}
	return module.NewRegistry(ticks...)
}
