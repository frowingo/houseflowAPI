package tests

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/application/game/module"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type gameConfigFixture struct {
	*gameSessionApplicationFixture
	configs *database.GameConfigRepository
	catalog *gameApplication.Catalog
	update  *gameCommands.UpdateGameConfigHandler
	ensure  *gameCommands.EnsureActiveGameSessionHandler
	command int
}

func newGameConfigFixture(t *testing.T) *gameConfigFixture {
	t.Helper()
	fixture := &gameConfigFixture{gameSessionApplicationFixture: newGameSessionApplicationFixture(t)}
	fixture.configs = database.NewGameConfigRepository(fixture.db.Client(), fixture.db.Name())
	fixture.catalog = gameApplication.NewCatalog(rocketsGames(), fixture.configs, nil)
	if err := fixture.catalog.Refresh(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	fixture.update = gameCommands.NewUpdateGameConfigHandler(fixture.catalog, fixture.configs, func(err error) { t.Error(err) })
	fixture.ensure = gameCommands.NewEnsureActiveGameSessionHandler(fixture.repository, fixture.catalog, fixture.membershipPolicy())
	return fixture
}

func (fixture *gameConfigFixture) ensureSession() (gameDomain.SessionSnapshot, error) {
	fixture.command++
	return fixture.ensure.Handle(fixture.ctx, gameCommands.EnsureActiveGameSessionCommand{CommandID: "config-ensure-" + string(rune('a'+fixture.command)), UserID: fixture.ownerID, HouseID: fixture.houseID, GameKey: houseRockets.GameKey})
}

func (fixture *gameConfigFixture) configure(t *testing.T, command gameCommands.UpdateGameConfigCommand) gameDomain.GameConfig {
	t.Helper()
	command.GameKey, command.UserID = houseRockets.GameKey, fixture.ownerID
	entry, err := fixture.update.Handle(fixture.ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	return entry.Config
}

// The seed must describe exactly what the previous release ran, so the first
// Release 1 instances behave like v37 until an operator changes something.
func TestGameConfigSeedMatchesCodeDefaults(t *testing.T) {
	fixture := newGameConfigFixture(t)
	config, err := fixture.catalog.Config(houseRockets.GameKey)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := houseRockets.Module().ResolveConfig(module.Config{Rules: houseRockets.Definition().Rules})
	if err != nil {
		t.Fatal(err)
	}
	var seeded, coded houseRockets.Settings
	_ = json.Unmarshal(config.Settings, &seeded)
	_ = json.Unmarshal(defaults.Settings, &coded)
	if config.Version != 1 || !config.Enabled || len(config.HouseIDs) != 0 || config.Rules != houseRockets.Definition().Rules || seeded != coded {
		t.Fatalf("seeded config = %+v %s", config, config.Settings)
	}
	var stored bson.M
	if err := fixture.db.Collection(database.GameConfigCollectionName).FindOne(fixture.ctx, bson.M{"_id": houseRockets.GameKey}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if _, integer := stored["settings"].(bson.M)["snapshotRateHz"].(int64); !integer {
		t.Fatalf("settings are not stored as editable integers: %#v", stored["settings"])
	}
}

// Switching a game off or narrowing its rollout stops new sessions only; a
// rules or settings change reaches the next session and never an existing one.
func TestGameConfigControlsNewSessionsOnly(t *testing.T) {
	fixture := newGameConfigFixture(t)
	first, err := fixture.ensureSession()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.repository.FindByID(fixture.ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConfigVersion != 1 || len(first.Settings) == 0 || !bytes.Equal(stored.Snapshot().Settings, first.Settings) || stored.Snapshot().ConfigVersion != 1 {
		t.Fatalf("frozen config: created %d %s, stored %d %s", first.ConfigVersion, first.Settings, stored.Snapshot().ConfigVersion, stored.Snapshot().Settings)
	}
	if first.Rules.MaximumPlayers != 4 {
		t.Fatalf("house capacity cap: %+v", first.Rules)
	}

	fixture.configure(t, gameCommands.UpdateGameConfigCommand{ExpectedVersion: 1, Enabled: false})
	if active, err := fixture.ensureSession(); err != nil || active.SessionID != first.SessionID {
		t.Fatalf("existing session after switch-off: %+v %v", active, err)
	}
	if _, err := gameQueries.NewGetActiveGameSessionHandler(fixture.repository, fixture.catalog, fixture.membershipPolicy()).Handle(fixture.ctx, gameQueries.GetActiveGameSessionQuery{HouseID: fixture.houseID, GameKey: houseRockets.GameKey, UserID: fixture.memberID}); err != nil {
		t.Fatalf("active session lookup after switch-off: %v", err)
	}
	if _, err := gameCommands.NewCancelGameSessionHandler(fixture.repository, fixture.membershipPolicy(), rocketsGames(), nil).Handle(fixture.ctx, gameCommands.CancelGameSessionCommand{CommandID: "config-cancel", SessionID: first.SessionID, UserID: fixture.ownerID}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.ensureSession(); !helpers.IsApplicationError(err, "game.error.unavailable") {
		t.Fatalf("new session of a switched-off game: %v", err)
	}

	otherHouse := primitive.NewObjectID().Hex()
	fixture.configure(t, gameCommands.UpdateGameConfigCommand{ExpectedVersion: 2, Enabled: true, HouseIDs: []string{otherHouse}})
	if _, err := fixture.ensureSession(); !helpers.IsApplicationError(err, "game.error.unavailable") {
		t.Fatalf("new session outside the rollout: %v", err)
	}
	catalogQuery := gameQueries.NewGetGameCatalogHandler(fixture.catalog, fixture.membershipPolicy())
	if entries, err := catalogQuery.Handle(fixture.ctx, gameQueries.GetGameCatalogQuery{HouseID: fixture.houseID, UserID: fixture.memberID}); err != nil || len(entries) != 0 {
		t.Fatalf("catalog outside the rollout: %+v %v", entries, err)
	}

	config := fixture.configure(t, gameCommands.UpdateGameConfigCommand{ExpectedVersion: 3, Enabled: true, HouseIDs: []string{fixture.houseID, otherHouse},
		Rules: json.RawMessage(`{"readyWindowMilliseconds":10000}`), Settings: json.RawMessage(`{"snapshotRateHz":30,"controlTimeoutMilliseconds":8000}`)})
	if config.Version != 4 || config.Rules.ReadyWindowDuration != 10*time.Second {
		t.Fatalf("updated config = %+v", config)
	}
	second, err := fixture.ensureSession()
	if err != nil {
		t.Fatal(err)
	}
	game := houseRockets.Module()
	welcome := game.Welcome(second.SessionID, "connection", second.Settings).(houseRockets.HouseRocketsWelcomeModel)
	if second.ConfigVersion != 4 || second.Rules.ReadyWindowDuration != 10*time.Second || welcome.Settings.SnapshotRateHz != 30 || game.Timing(second.Settings).ControlTimeout != 8*time.Second {
		t.Fatalf("second session = %+v %s", second, second.Settings)
	}
	if reread, err := fixture.repository.FindByID(fixture.ctx, first.SessionID); err != nil || reread.Snapshot().ConfigVersion != 1 || !bytes.Equal(reread.Snapshot().Settings, first.Settings) {
		t.Fatalf("existing session changed: %v", err)
	}

	entries, err := catalogQuery.Handle(fixture.ctx, gameQueries.GetGameCatalogQuery{HouseID: fixture.houseID, UserID: fixture.memberID})
	if err != nil || len(entries) != 1 {
		t.Fatalf("catalog = %+v %v", entries, err)
	}
	model, _ := json.Marshal(dtos.GameCatalogEntryToModel(entries[0].Definition, entries[0].Config, entries[0].ClientSettings))
	var public struct {
		GameKey       string `json:"gameKey"`
		ConfigVersion int64  `json:"configVersion"`
		Rules         struct {
			MaximumPlayers          int   `json:"maximumPlayers"`
			ReadyWindowMilliseconds int64 `json:"readyWindowMilliseconds"`
		} `json:"rules"`
		Settings houseRockets.HouseRocketsRuntimeSettingsModel `json:"settings"`
	}
	if err := json.Unmarshal(model, &public); err != nil || public.GameKey != houseRockets.GameKey || public.ConfigVersion != 4 || public.Rules.MaximumPlayers != 4 ||
		public.Rules.ReadyWindowMilliseconds != 10_000 || public.Settings.SnapshotRateHz != 30 || public.Settings.PhysicsRateHz != houseRockets.PhysicsRateHz {
		t.Fatalf("catalog model = %s %v", model, err)
	}
	if _, err := catalogQuery.Handle(fixture.ctx, gameQueries.GetGameCatalogQuery{HouseID: fixture.houseID, UserID: fixture.outsiderID}); err == nil {
		t.Fatal("outsider read the house catalog")
	}
}

// Writes are validated before they are stored and fenced by version.
func TestGameConfigRejectsInvalidAndStaleWrites(t *testing.T) {
	fixture := newGameConfigFixture(t)
	for name, command := range map[string]gameCommands.UpdateGameConfigCommand{
		"unknownSetting": {ExpectedVersion: 1, Enabled: true, Settings: json.RawMessage(`{"snapshotRate":30}`)},
		"outOfRange":     {ExpectedVersion: 1, Enabled: true, Settings: json.RawMessage(`{"snapshotRateHz":500}`)},
		"tooManyPlayers": {ExpectedVersion: 1, Enabled: true, Rules: json.RawMessage(`{"maximumPlayers":9}`)},
	} {
		command.GameKey, command.UserID = houseRockets.GameKey, fixture.ownerID
		if _, err := fixture.update.Handle(fixture.ctx, command); !helpers.IsApplicationError(err, "game.error.invalid_config") {
			t.Errorf("%s: %v", name, err)
		}
	}
	unknown := gameCommands.UpdateGameConfigCommand{GameKey: "unknownGame", UserID: fixture.ownerID, ExpectedVersion: 0, Enabled: true}
	if _, err := fixture.update.Handle(fixture.ctx, unknown); !helpers.IsApplicationError(err, "game.error.definition_not_found") {
		t.Fatalf("unknown game: %v", err)
	}
	fixture.configure(t, gameCommands.UpdateGameConfigCommand{ExpectedVersion: 1, Enabled: true, Settings: json.RawMessage(`{"snapshotRateHz":25}`)})
	stale := gameCommands.UpdateGameConfigCommand{GameKey: houseRockets.GameKey, UserID: fixture.ownerID, ExpectedVersion: 1, Enabled: false}
	if _, err := fixture.update.Handle(fixture.ctx, stale); !helpers.IsApplicationError(err, "game.error.config_conflict") {
		t.Fatalf("stale write: %v", err)
	}
	if config, _ := fixture.catalog.Config(houseRockets.GameKey); config.Version != 2 || !config.Enabled {
		t.Fatalf("config after rejected writes = %+v", config)
	}
}

// An operator editing the collection directly can break a document; the
// catalog then keeps serving the last good config. A registered game without
// a document never starts sessions.
func TestGameConfigKeepsLastGoodConfigAndDefaultsToDisabled(t *testing.T) {
	fixture := newGameConfigFixture(t)
	games := fixture.db.Collection(database.GameConfigCollectionName)
	if _, err := games.UpdateOne(fixture.ctx, bson.M{"_id": houseRockets.GameKey}, bson.M{"$set": bson.M{"settings.snapshotRateHz": 0.5, "version": int64(2)}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.catalog.Refresh(fixture.ctx); err == nil {
		t.Fatal("invalid document was not reported")
	}
	if config, _ := fixture.catalog.Config(houseRockets.GameKey); config.Version != 1 || !config.Enabled {
		t.Fatalf("config after invalid document = %+v", config)
	}
	// A shell writes whole numbers as doubles; they are read as integers.
	if _, err := games.UpdateOne(fixture.ctx, bson.M{"_id": houseRockets.GameKey}, bson.M{"$set": bson.M{"settings.snapshotRateHz": float64(15), "version": int64(3)}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.catalog.Refresh(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if config, _ := fixture.catalog.Config(houseRockets.GameKey); config.Version != 3 || !bytes.Contains(config.Settings, []byte(`"snapshotRateHz":15`)) {
		t.Fatalf("config after shell edit = %+v %s", config, config.Settings)
	}

	if _, err := games.DeleteOne(fixture.ctx, bson.M{"_id": houseRockets.GameKey}); err != nil {
		t.Fatal(err)
	}
	fresh := gameApplication.NewCatalog(rocketsGames(), fixture.configs, nil)
	if config, _ := fresh.Config(houseRockets.GameKey); config.Enabled {
		t.Fatal("unloaded catalog enabled a game")
	}
	if err := fresh.Refresh(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if config, err := fresh.Config(houseRockets.GameKey); err != nil || config.Enabled || config.Version != 0 {
		t.Fatalf("game without a document = %+v %v", config, err)
	}
	if _, err := fresh.Find(houseRockets.GameKey); err != nil {
		t.Fatalf("a switched-off game must stay registered: %v", err)
	}
}

// A change written on one instance reaches another through Redis well before
// the periodic reload.
func TestGameConfigChangeReachesOtherInstances(t *testing.T) {
	fixture := newGameConfigFixture(t)
	redisURL := prepareRedis(t)
	writer := gameApplication.NewCatalog(rocketsGames(), fixture.configs, newTestCoordinator(t, redisURL, "config-writer"))
	reader := gameApplication.NewCatalog(rocketsGames(), fixture.configs, newTestCoordinator(t, redisURL, "config-reader"))
	for _, catalog := range []*gameApplication.Catalog{writer, reader} {
		go catalog.Run(fixture.ctx, func(err error) { t.Error(err) })
	}
	waitFor := func(catalog *gameApplication.Catalog, version int64, within time.Duration) {
		t.Helper()
		deadline := time.Now().Add(within)
		for time.Now().Before(deadline) {
			if config, _ := catalog.Config(houseRockets.GameKey); config.Version == version {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		config, _ := catalog.Config(houseRockets.GameKey)
		t.Fatalf("config version %d, want %d", config.Version, version)
	}
	waitFor(reader, 1, 2*time.Second)
	// Let the reader's subscription settle before the announcement.
	time.Sleep(200 * time.Millisecond)
	update := gameCommands.NewUpdateGameConfigHandler(writer, fixture.configs, func(err error) { t.Error(err) })
	started := time.Now()
	if _, err := update.Handle(fixture.ctx, gameCommands.UpdateGameConfigCommand{GameKey: houseRockets.GameKey, UserID: fixture.ownerID, ExpectedVersion: 1, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	waitFor(writer, 2, 10*time.Millisecond)
	waitFor(reader, 2, 2*time.Second)
	if elapsed := time.Since(started); elapsed >= gameApplication.CatalogRefreshInterval {
		t.Fatalf("change arrived by polling after %v", elapsed)
	}
	if config, _ := reader.Config(houseRockets.GameKey); config.AvailableTo(fixture.houseID) {
		t.Fatal("reader still offers the switched-off game")
	}
}

var _ gameAbstract.GameConfigRepository = (*database.GameConfigRepository)(nil)
