package game

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
)

const (
	// CatalogRefreshInterval bounds how long an instance that missed a change
	// announcement keeps serving the previous config.
	CatalogRefreshInterval = 30 * time.Second
	catalogRetryInterval   = 2 * time.Second
)

// CatalogEntry is a registered game with the config new sessions start with.
type CatalogEntry struct {
	Definition     gameDomain.GameDefinition
	Config         gameDomain.GameConfig
	ClientSettings any
}

// Catalog serves the games registered in code with the config stored in the
// Game collection. Every instance keeps it in memory, reloads it on an
// interval and immediately when another instance announces a change. A game
// without a stored document, or whose document never resolved, is disabled.
type Catalog struct {
	games      *module.Registry
	repository gameAbstract.GameConfigRepository
	bus        coordinationAbstract.GameConfigBus
	refresh    sync.Mutex
	configs    atomic.Pointer[map[string]gameDomain.GameConfig]
	loaded     atomic.Bool
}

// NewCatalog without a repository serves every registered game enabled with
// its defaults; the bus is optional.
func NewCatalog(games *module.Registry, repository gameAbstract.GameConfigRepository, bus coordinationAbstract.GameConfigBus) *Catalog {
	catalog := &Catalog{games: games, repository: repository, bus: bus}
	configs := make(map[string]gameDomain.GameConfig)
	for _, game := range games.Games() {
		config, _ := resolveConfig(game, gameAbstract.GameConfigRecord{Enabled: repository == nil})
		configs[game.Definition().GameKey] = config
	}
	catalog.configs.Store(&configs)
	catalog.loaded.Store(repository == nil)
	return catalog
}

func (catalog *Catalog) Find(gameKey string) (gameDomain.GameDefinition, error) {
	game, exists := catalog.games.Game(gameKey)
	if !exists {
		return gameDomain.GameDefinition{}, gameAbstract.ErrGameDefinitionNotFound
	}
	return game.Definition(), nil
}

func (catalog *Catalog) Config(gameKey string) (gameDomain.GameConfig, error) {
	config, exists := (*catalog.configs.Load())[gameKey]
	if !exists {
		return gameDomain.GameConfig{}, gameAbstract.ErrGameDefinitionNotFound
	}
	return config, nil
}

// Entries lists every registered game ordered by key.
func (catalog *Catalog) Entries() []CatalogEntry {
	configs := *catalog.configs.Load()
	games := catalog.games.Games()
	entries := make([]CatalogEntry, 0, len(games))
	for _, game := range games {
		definition := game.Definition()
		config := configs[definition.GameKey]
		entries = append(entries, CatalogEntry{Definition: definition, Config: config, ClientSettings: game.ClientSettings(config.Settings)})
	}
	return entries
}

// Resolve validates a stored or proposed document against its module.
func (catalog *Catalog) Resolve(record gameAbstract.GameConfigRecord) (gameDomain.GameConfig, error) {
	game, exists := catalog.games.Game(record.GameKey)
	if !exists {
		return gameDomain.GameConfig{}, gameAbstract.ErrGameDefinitionNotFound
	}
	return resolveConfig(game, record)
}

// Refresh reloads every document. A document that no longer resolves keeps
// the game on its last good config and is reported in the returned error.
func (catalog *Catalog) Refresh(ctx context.Context) error {
	if catalog.repository == nil {
		return nil
	}
	catalog.refresh.Lock()
	defer catalog.refresh.Unlock()
	records, err := catalog.repository.List(ctx)
	if err != nil {
		return err
	}
	stored := make(map[string]gameAbstract.GameConfigRecord, len(records))
	for _, record := range records {
		stored[record.GameKey] = record
	}
	previous := *catalog.configs.Load()
	next := make(map[string]gameDomain.GameConfig, len(previous))
	var invalid []error
	for _, game := range catalog.games.Games() {
		gameKey := game.Definition().GameKey
		record, exists := stored[gameKey]
		config, err := resolveConfig(game, record)
		if exists && err != nil {
			invalid = append(invalid, fmt.Errorf("game config %q version %d: %w", gameKey, record.Version, err))
			config = previous[gameKey]
		}
		next[gameKey] = config
	}
	catalog.configs.Store(&next)
	catalog.loaded.Store(true)
	return errors.Join(invalid...)
}

// Changed reloads this instance and tells the others after a config write.
func (catalog *Catalog) Changed(ctx context.Context, gameKey string) error {
	refreshErr := catalog.Refresh(ctx)
	if catalog.bus == nil {
		return refreshErr
	}
	return errors.Join(refreshErr, catalog.bus.PublishGameConfigChanged(ctx, gameKey))
}

// Run keeps the catalog current until ctx ends. Announcements trigger an
// immediate reload; the interval catches announcements lost while Redis or
// this subscription was unavailable.
func (catalog *Catalog) Run(ctx context.Context, report func(error)) {
	if catalog.repository == nil {
		return
	}
	var subscription coordinationAbstract.RoomEventSubscription
	defer func() {
		if subscription != nil {
			_ = subscription.Close()
		}
	}()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		var changes <-chan coordinationAbstract.MessageEnvelope
		var failures <-chan error
		if subscription == nil && catalog.bus != nil {
			if subscribed, err := catalog.bus.SubscribeGameConfigChanges(ctx); err == nil {
				subscription = subscribed
				resetTimer(timer, 0)
			}
		}
		if subscription != nil {
			changes, failures = subscription.Messages(), subscription.Errors()
		}
		select {
		case <-ctx.Done():
			return
		case _, open := <-changes:
			if !open {
				subscription = nil
				continue
			}
		case _, open := <-failures:
			if !open {
				subscription = nil
				continue
			}
			// A reconnect may have dropped announcements.
		case <-timer.C:
		}
		err := catalog.Refresh(ctx)
		if err != nil && ctx.Err() == nil {
			report(err)
		}
		delay := CatalogRefreshInterval
		if !catalog.loaded.Load() {
			delay = catalogRetryInterval
		}
		resetTimer(timer, delay)
	}
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

// storedRules mirrors the rules object of a Game document.
type storedRules struct {
	MinimumPlayers          int   `json:"minimumPlayers"`
	MaximumPlayers          int   `json:"maximumPlayers"`
	ReadyWindowMilliseconds int64 `json:"readyWindowMilliseconds"`
	CountdownMilliseconds   int64 `json:"countdownMilliseconds"`
}

func resolveConfig(game module.Game, record gameAbstract.GameConfigRecord) (gameDomain.GameConfig, error) {
	defaults := game.Definition().Rules
	rules := storedRules{defaults.MinimumPlayers, defaults.MaximumPlayers, defaults.ReadyWindowDuration.Milliseconds(), defaults.CountdownDuration.Milliseconds()}
	if trimmed := bytes.TrimSpace(record.Rules); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&rules); err != nil {
			return gameDomain.GameConfig{}, errors.Join(module.ErrInvalidConfig, err)
		}
		if err := helpers.EnsureJSONEnd(decoder); err != nil {
			return gameDomain.GameConfig{}, errors.Join(module.ErrInvalidConfig, err)
		}
	}
	resolved, err := game.ResolveConfig(module.Config{Settings: record.Settings, Rules: gameDomain.SessionRules{
		MinimumPlayers:      rules.MinimumPlayers,
		MaximumPlayers:      rules.MaximumPlayers,
		ReadyWindowDuration: time.Duration(rules.ReadyWindowMilliseconds) * time.Millisecond,
		CountdownDuration:   time.Duration(rules.CountdownMilliseconds) * time.Millisecond,
	}})
	if err != nil {
		return gameDomain.GameConfig{}, err
	}
	houseIDs := make([]string, 0, len(record.HouseIDs))
	for _, houseID := range record.HouseIDs {
		if houseID == "" {
			return gameDomain.GameConfig{}, errors.Join(module.ErrInvalidConfig, errors.New("rollout house ID is empty"))
		}
		if !slices.Contains(houseIDs, houseID) {
			houseIDs = append(houseIDs, houseID)
		}
	}
	settings, err := helpers.CanonicalJSONObject(resolved.Settings)
	if err != nil {
		return gameDomain.GameConfig{}, errors.Join(module.ErrInvalidConfig, err)
	}
	return gameDomain.GameConfig{Version: record.Version, Enabled: record.Enabled, HouseIDs: houseIDs, Rules: resolved.Rules, Settings: settings}, nil
}

var _ gameAbstract.GameCatalog = (*Catalog)(nil)
