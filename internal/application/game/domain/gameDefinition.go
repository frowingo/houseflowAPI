package domain

import (
	"encoding/json"
	"slices"
)

type GameDefinition struct {
	GameKey         string
	ProtocolVersion int
	Mode            GameMode
	Rules           SessionRules
}

func (definition GameDefinition) Validate() error {
	switch {
	case definition.GameKey == "":
		return ErrGameKeyRequired
	case definition.ProtocolVersion <= 0:
		return ErrProtocolVersion
	case !isSupportedGameMode(definition.Mode):
		return ErrGameMode
	default:
		return definition.Rules.Validate()
	}
}

// GameConfig is the operator-controlled state of a game: whether new sessions
// may start, for which houses, and the resolved rules and settings they freeze.
type GameConfig struct {
	Version int64
	Enabled bool
	// HouseIDs limits a staged rollout to the listed houses; empty means all.
	HouseIDs []string
	Rules    SessionRules
	Settings json.RawMessage
}

func (config GameConfig) AvailableTo(houseID string) bool {
	return config.Enabled && (len(config.HouseIDs) == 0 || slices.Contains(config.HouseIDs, houseID))
}
