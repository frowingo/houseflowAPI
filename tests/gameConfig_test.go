package tests

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/application/game/module"
)

// House Rockets settings change transport and presence limits only; values
// that checkpoints and results are validated against are not configurable.
func TestHouseRocketsConfigBounds(t *testing.T) {
	game := houseRockets.Module()
	rules := game.Definition().Rules
	for name, settings := range map[string]string{
		"snapshotAboveScheduler":   `{"snapshotRateHz":61}`,
		"inputAboveSteerLimit":     `{"maximumInputRateHz":40,"maximumSteerMessagesPerSecond":30}`,
		"timeoutBelowTwoBeats":     `{"controlHeartbeatMilliseconds":2000,"controlTimeoutMilliseconds":3999}`,
		"negativeGrace":            `{"reconnectGraceMilliseconds":-1}`,
		"contractValue":            `{"physicsRateHz":60}`,
		"matchLengthIsContract":    `{"maximumMatchMilliseconds":60000}`,
		"fractionalRate":           `{"snapshotRateHz":20.5}`,
		"trailingValue":            `{"snapshotRateHz":20} {}`,
		"notAnObject":              `[20]`,
		"heartbeatAboveTenSeconds": `{"controlHeartbeatMilliseconds":10001,"controlTimeoutMilliseconds":30000}`,
	} {
		if _, err := game.ResolveConfig(module.Config{Rules: rules, Settings: json.RawMessage(settings)}); !errors.Is(err, module.ErrInvalidConfig) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	for name, change := range map[string]func(*module.Config){
		"morePlayersThanColors": func(config *module.Config) { config.Rules.MaximumPlayers = houseRockets.MaximumPlayers + 1 },
		"noCountdown":           func(config *module.Config) { config.Rules.CountdownDuration = 0 },
		"longReadyWindow":       func(config *module.Config) { config.Rules.ReadyWindowDuration = 3 * time.Minute },
		"subMillisecond":        func(config *module.Config) { config.Rules.CountdownDuration = 2*time.Second + time.Microsecond },
	} {
		config := module.Config{Rules: rules}
		change(&config)
		if _, err := game.ResolveConfig(config); !errors.Is(err, module.ErrInvalidConfig) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	// Absent keys keep their defaults.
	resolved, err := game.ResolveConfig(module.Config{Rules: rules, Settings: json.RawMessage(`{"snapshotRateHz":30}`)})
	if err != nil {
		t.Fatal(err)
	}
	var settings houseRockets.Settings
	if err := json.Unmarshal(resolved.Settings, &settings); err != nil {
		t.Fatal(err)
	}
	expected := houseRockets.DefaultSettings()
	expected.SnapshotRateHz = 30
	if settings != expected {
		t.Fatalf("settings = %+v, want %+v", settings, expected)
	}
}

// A session's frozen settings reach the welcome, the transport limits and the
// runtime; the defaults reach every session that has none.
func TestHouseRocketsRuntimeFollowsSessionSettings(t *testing.T) {
	game := houseRockets.Module()
	frozen := json.RawMessage(`{"snapshotRateHz":10,"maximumInputRateHz":3,"maximumSteerMessagesPerSecond":3,"controlHeartbeatMilliseconds":500,"controlTimeoutMilliseconds":1000,"reconnectGraceMilliseconds":2000}`)
	welcome := game.Welcome("settings-session", "settings-connection", frozen).(houseRockets.HouseRocketsWelcomeModel)
	if welcome.Settings.SnapshotRateHz != 10 || welcome.Settings.ControlTimeoutMilliseconds != 1000 || welcome.Settings.PhysicsRateHz != houseRockets.PhysicsRateHz {
		t.Fatalf("welcome settings = %+v", welcome.Settings)
	}
	if defaults := game.Welcome("settings-session", "settings-connection", nil).(houseRockets.HouseRocketsWelcomeModel); defaults.Settings != houseRockets.HouseRocketsRuntimeSettings() {
		t.Fatalf("default welcome settings = %+v", defaults.Settings)
	}
	if timing := game.Timing(frozen); timing.ControlTimeout != time.Second || timing.MaximumIntentRate != 3 || timing.InputLifetime != houseRockets.InputLifetime {
		t.Fatalf("timing = %+v", timing)
	}

	now := time.Now()
	start := func() (module.TickRuntime, module.ControlGrant) {
		runtime, err := game.NewRuntime(module.RuntimeParams{SessionID: "settings-session", HouseID: "settings-house", StartedAt: now, Epoch: 3, Now: now, Settings: frozen,
			Players: []module.Player{{PlayerID: "first", DisplayName: "First"}, {PlayerID: "second", DisplayName: "Second"}}})
		if err != nil {
			t.Fatal(err)
		}
		bind, _ := json.Marshal(module.ControlInput{Kind: module.InputBind, PlayerID: "first", ConnectionID: "first-connection", RuntimeEpoch: 3, CreatedAt: now})
		if err := runtime.Submit(bind, now); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Advance(now); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for {
			update, err := runtime.NextUpdate(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if update.Grant != nil {
				return runtime, *update.Grant
			}
		}
	}
	steer := func(grant module.ControlGrant, sequence int64, at time.Time) json.RawMessage {
		input, _ := json.Marshal(houseRockets.RuntimeInput{Kind: houseRockets.InputSteer, PlayerID: grant.PlayerID, ConnectionID: "first-connection", RuntimeEpoch: grant.RuntimeEpoch, ControlGeneration: grant.ControlGeneration, InputSequence: sequence, CreatedAt: at})
		return input
	}

	runtime, grant := start()
	for sequence := int64(1); sequence <= 3; sequence++ {
		if err := runtime.Submit(steer(grant, sequence, now), now); err != nil {
			t.Fatalf("steer %d: %v", sequence, err)
		}
	}
	if err := runtime.Submit(steer(grant, 4, now), now); !errors.Is(err, module.ErrInputRate) {
		t.Fatalf("fourth steer in a second: %v", err)
	}

	// The default six-second control timeout would still accept this input.
	runtime, grant = start()
	late := now.Add(1100 * time.Millisecond)
	if err := runtime.Submit(steer(grant, 1, late), late); !errors.Is(err, module.ErrControlRequired) {
		t.Fatalf("steer after the session's control timeout: %v", err)
	}
}

// A catalog without a repository serves every registered game with its
// defaults; validation of documents runs through the module.
func TestCatalogResolvesDocumentsThroughTheModule(t *testing.T) {
	catalog := gameApplication.NewCatalog(rocketsGames(), nil, nil)
	config, err := catalog.Config(houseRockets.GameKey)
	if err != nil || !config.Enabled || config.Rules != houseRockets.Definition().Rules || !config.AvailableTo("any-house") {
		t.Fatalf("static config = %+v %v", config, err)
	}
	if _, err := catalog.Config("unknownGame"); !errors.Is(err, gameAbstract.ErrGameDefinitionNotFound) {
		t.Fatalf("unknown game: %v", err)
	}
	for name, record := range map[string]gameAbstract.GameConfigRecord{
		"unknownRule":    {GameKey: houseRockets.GameKey, Rules: json.RawMessage(`{"minimumPlayer":2}`)},
		"emptyHouse":     {GameKey: houseRockets.GameKey, HouseIDs: []string{""}},
		"invalidSetting": {GameKey: houseRockets.GameKey, Settings: json.RawMessage(`{"snapshotRateHz":0}`)},
	} {
		if _, err := catalog.Resolve(record); !errors.Is(err, module.ErrInvalidConfig) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	resolved, err := catalog.Resolve(gameAbstract.GameConfigRecord{GameKey: houseRockets.GameKey, Version: 9, Enabled: true, HouseIDs: []string{"a", "b", "a"},
		Rules: json.RawMessage(`{"maximumPlayers":4,"readyWindowMilliseconds":10000}`)})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Version != 9 || len(resolved.HouseIDs) != 2 || resolved.Rules.MaximumPlayers != 4 || resolved.Rules.ReadyWindowDuration != 10*time.Second ||
		resolved.Rules.MinimumPlayers != houseRockets.MinimumPlayers || resolved.AvailableTo("c") || !resolved.AvailableTo("b") {
		t.Fatalf("resolved = %+v", resolved)
	}
}
