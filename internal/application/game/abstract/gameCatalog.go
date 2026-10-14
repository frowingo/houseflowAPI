package abstract

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

var (
	ErrGameDefinitionNotFound = errors.New("game definition was not found")
	ErrGameUnavailable        = errors.New("game is not available to this house")
	ErrGameConfigConflict     = errors.New("game config was changed concurrently")
)

// GameCatalog knows the games this process can run and their current config.
type GameCatalog interface {
	// Find returns a registered game whether or not it accepts new sessions,
	// so existing sessions stay reachable after a game is switched off.
	Find(gameKey string) (gameDomain.GameDefinition, error)
	// Config returns the config new sessions of a registered game start with.
	Config(gameKey string) (gameDomain.GameConfig, error)
}

// GameConfigRecord is one Game document. Rules and Settings are partial JSON
// objects laid over the module defaults, so a key absent from the document
// keeps its code default.
type GameConfigRecord struct {
	GameKey   string
	Version   int64
	Enabled   bool
	HouseIDs  []string
	Rules     json.RawMessage
	Settings  json.RawMessage
	UpdatedAt time.Time
	UpdatedBy string
}

type GameConfigRepository interface {
	List(ctx context.Context) ([]GameConfigRecord, error)
	// Save stores record as version expectedVersion+1. Zero creates the
	// document; a stale version returns ErrGameConfigConflict.
	Save(ctx context.Context, record GameConfigRecord, expectedVersion int64) (GameConfigRecord, error)
}
