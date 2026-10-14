package module

import (
	"errors"
	"fmt"
	"sort"

	gameDomain "houseflowApi/internal/application/game/domain"
)

// Registry holds the games enabled in this process, by lane. The core resolves
// every game-specific behavior through it; an unknown key has no module.
type Registry struct {
	games map[string]Game
}

// NewRegistry accepts tick and turn modules; each must declare its lane's mode.
func NewRegistry(games ...Game) (*Registry, error) {
	registry := &Registry{games: make(map[string]Game, len(games))}
	for _, game := range games {
		definition := game.Definition()
		if err := definition.Validate(); err != nil {
			return nil, fmt.Errorf("game %q: %w", definition.GameKey, err)
		}
		if _, exists := registry.games[definition.GameKey]; exists {
			return nil, errors.New("duplicate game module " + definition.GameKey)
		}
		_, tick := game.(TickModule)
		_, turn := game.(TurnModule)
		switch {
		case tick && !turn && definition.Mode == gameDomain.RealtimeGame:
		case turn && !tick && definition.Mode == gameDomain.TurnBasedGame:
		default:
			return nil, fmt.Errorf("game %q: module lane does not match mode %q", definition.GameKey, definition.Mode)
		}
		if _, err := game.ResolveConfig(Config{Rules: definition.Rules}); err != nil {
			return nil, fmt.Errorf("game %q defaults: %w", definition.GameKey, err)
		}
		registry.games[definition.GameKey] = game
	}
	return registry, nil
}

// Game returns the lane-independent part of a registered module. A nil
// registry has none.
func (registry *Registry) Game(gameKey string) (Game, bool) {
	if registry == nil {
		return nil, false
	}
	game, exists := registry.games[gameKey]
	return game, exists
}

// Tick returns the module of a realtime tick game.
func (registry *Registry) Tick(gameKey string) (TickModule, bool) {
	game, _ := registry.Game(gameKey)
	tick, exists := game.(TickModule)
	return tick, exists
}

// Turn returns the module of a turn-based game.
func (registry *Registry) Turn(gameKey string) (TurnModule, bool) {
	game, _ := registry.Game(gameKey)
	turn, exists := game.(TurnModule)
	return turn, exists
}

// Games lists every registered module ordered by game key.
func (registry *Registry) Games() []Game {
	if registry == nil {
		return nil
	}
	games := make([]Game, 0, len(registry.games))
	for _, game := range registry.games {
		games = append(games, game)
	}
	sort.Slice(games, func(i, j int) bool { return games[i].Definition().GameKey < games[j].Definition().GameKey })
	return games
}

// HasRuntime reports whether running sessions of the game are completed by a
// tick runtime instead of a durable session command.
func (registry *Registry) HasRuntime(gameKey string) bool {
	_, exists := registry.Tick(gameKey)
	return exists
}

func (registry *Registry) HasTickGames() bool {
	for _, game := range registry.Games() {
		if _, tick := game.(TickModule); tick {
			return true
		}
	}
	return false
}

// HasRealtimeGames reports whether any registered game is played over the
// realtime gateway; every lane is.
func (registry *Registry) HasRealtimeGames() bool { return len(registry.Games()) > 0 }

func (registry *Registry) Results(gameKey string) (ResultCodec, bool) {
	return registry.Game(gameKey)
}

func (registry *Registry) Definitions() []gameDomain.GameDefinition {
	games := registry.Games()
	definitions := make([]gameDomain.GameDefinition, 0, len(games))
	for _, game := range games {
		definitions = append(definitions, game.Definition())
	}
	return definitions
}
