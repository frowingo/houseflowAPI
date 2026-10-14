package module

import (
	"errors"
	"fmt"
	"sort"

	gameDomain "houseflowApi/internal/application/game/domain"
)

// Registry holds the games enabled in this process. The core resolves every
// game-specific behavior through it; an unknown key has no runtime.
type Registry struct {
	ticks map[string]TickModule
}

func NewRegistry(ticks ...TickModule) (*Registry, error) {
	registry := &Registry{ticks: make(map[string]TickModule, len(ticks))}
	for _, game := range ticks {
		definition := game.Definition()
		if err := definition.Validate(); err != nil {
			return nil, fmt.Errorf("game %q: %w", definition.GameKey, err)
		}
		if _, exists := registry.ticks[definition.GameKey]; exists {
			return nil, errors.New("duplicate game module " + definition.GameKey)
		}
		if _, err := game.ResolveConfig(Config{Rules: definition.Rules}); err != nil {
			return nil, fmt.Errorf("game %q defaults: %w", definition.GameKey, err)
		}
		registry.ticks[definition.GameKey] = game
	}
	return registry, nil
}

// Tick returns the realtime module for a game key. A nil registry has none.
func (registry *Registry) Tick(gameKey string) (TickModule, bool) {
	if registry == nil {
		return nil, false
	}
	game, exists := registry.ticks[gameKey]
	return game, exists
}

// Game returns the lane-independent part of a registered module.
func (registry *Registry) Game(gameKey string) (Game, bool) {
	return registry.Tick(gameKey)
}

// Games lists every registered module ordered by game key.
func (registry *Registry) Games() []Game {
	if registry == nil {
		return nil
	}
	games := make([]Game, 0, len(registry.ticks))
	for _, game := range registry.ticks {
		games = append(games, game)
	}
	sort.Slice(games, func(i, j int) bool { return games[i].Definition().GameKey < games[j].Definition().GameKey })
	return games
}

// HasRuntime reports whether running sessions of the game are completed by a
// runtime instead of a direct session command.
func (registry *Registry) HasRuntime(gameKey string) bool {
	_, exists := registry.Tick(gameKey)
	return exists
}

func (registry *Registry) HasTickGames() bool { return registry != nil && len(registry.ticks) > 0 }

func (registry *Registry) Results(gameKey string) (ResultCodec, bool) {
	return registry.Tick(gameKey)
}

func (registry *Registry) Definitions() []gameDomain.GameDefinition {
	games := registry.Games()
	definitions := make([]gameDomain.GameDefinition, 0, len(games))
	for _, game := range games {
		definitions = append(definitions, game.Definition())
	}
	return definitions
}
