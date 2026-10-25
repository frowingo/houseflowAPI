package tests

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	"houseflowApi/internal/application/game/games"
	"houseflowApi/internal/application/game/games/houseTanks"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/dtos"
)

type houseTanksProtocolFixture struct {
	SchemaVersion        int    `json:"schemaVersion"`
	ContractRevision     string `json:"contractRevision"`
	ProtocolVersion      int    `json:"protocolVersion"`
	MapCatalogVersion    int    `json:"mapCatalogVersion"`
	LiveGatewayAvailable bool   `json:"liveGatewayAvailable"`
	Rules                struct {
		MinimumPlayers          int   `json:"minimumPlayers"`
		MaximumPlayers          int   `json:"maximumPlayers"`
		ReadyWindowMilliseconds int64 `json:"readyWindowMilliseconds"`
		CountdownMilliseconds   int64 `json:"countdownMilliseconds"`
		houseTanks.HouseTanksRuntimeSettingsModel
		ExpiryEndReason   houseTanks.EndReason `json:"expiryEndReason"`
		ExpiryHasChampion bool                 `json:"expiryHasChampion"`
	} `json:"rules"`
	PlayerColors   []houseTanks.Color       `json:"playerColors"`
	PowerUps       []houseTanks.PowerUpKind `json:"powerUps"`
	MapIDs         []houseTanks.MapID       `json:"mapIds"`
	ClientMessages []struct {
		Name    string          `json:"name"`
		Message json.RawMessage `json:"message"`
	} `json:"clientMessages"`
	ServerMessages []struct {
		Name    string          `json:"name"`
		Message json.RawMessage `json:"message"`
	} `json:"serverMessages"`
	InvalidClientMessages []struct {
		Name      string `json:"name"`
		JSON      string `json:"json"`
		ErrorCode string `json:"errorCode"`
	} `json:"invalidClientMessages"`
}

func readHouseTanksFixture[T any](t *testing.T, name string) T {
	t.Helper()
	data, err := os.ReadFile("../internal/application/game/games/houseTanks/fixtures/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture T
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func decodedTanksInput(decoded realtime.DecodedV2ClientMessage) *houseTanks.RuntimeInput {
	if decoded.Intent == nil {
		return nil
	}
	input := decoded.Intent.RuntimeInput(module.ControlInput{}).(houseTanks.RuntimeInput)
	return &input
}

func TestHouseTanksDefinitionContract(t *testing.T) {
	fixture := readHouseTanksFixture[houseTanksProtocolFixture](t, "houseTanksProtocol")
	definition := houseTanks.Definition()
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	if fixture.SchemaVersion != 1 || fixture.ContractRevision != houseTanks.ContractRevision || fixture.ProtocolVersion != definition.ProtocolVersion ||
		fixture.MapCatalogVersion != houseTanks.MapCatalogVersion || realtime.V2ProtocolVersion != definition.ProtocolVersion {
		t.Fatal("fixture revisions and backend definition differ")
	}
	if definition.GameKey != "houseTanks" || definition.Rules.MinimumPlayers != 2 || definition.Rules.MaximumPlayers != 8 ||
		fixture.Rules.MinimumPlayers != definition.Rules.MinimumPlayers || fixture.Rules.MaximumPlayers != definition.Rules.MaximumPlayers ||
		fixture.Rules.ReadyWindowMilliseconds != definition.Rules.ReadyWindowDuration.Milliseconds() ||
		fixture.Rules.CountdownMilliseconds != definition.Rules.CountdownDuration.Milliseconds() {
		t.Fatal("player or lobby policy changed")
	}
	if !reflect.DeepEqual(fixture.Rules.HouseTanksRuntimeSettingsModel, houseTanks.HouseTanksRuntimeSettings()) ||
		fixture.Rules.ExpiryHasChampion || fixture.Rules.ExpiryEndReason != houseTanks.EndSessionExpired {
		t.Fatal("runtime settings or expiry policy changed")
	}
	settings := houseTanks.HouseTanksRuntimeSettings()
	if settings.ArenaWidth != 872 || settings.ArenaHeight != 400 || settings.RoundWinsNeeded != 3 || settings.ArmorPerRound != 5 ||
		settings.PhysicsRateHz != 120 || settings.MaximumMatchMilliseconds != 5*60*1000 {
		t.Fatal("approved arena or match rules changed")
	}
	if !reflect.DeepEqual(fixture.PlayerColors, houseTanks.PlayerColors()) || len(fixture.PlayerColors) != houseTanks.MaximumPlayers {
		t.Fatal("eight stable color identities are required in seat order")
	}
	colors := houseTanks.PlayerColors()
	colors[0] = houseTanks.ColorIvory
	if houseTanks.PlayerColors()[0] != houseTanks.ColorPlum {
		t.Fatal("colors share mutable storage")
	}
	if !reflect.DeepEqual(fixture.PowerUps, houseTanks.PowerUpKinds()) ||
		!reflect.DeepEqual(fixture.MapIDs, []houseTanks.MapID{houseTanks.MapLivingRoom, houseTanks.MapCorridors, houseTanks.MapWarehouse, houseTanks.MapCourtyard, houseTanks.MapBasement}) {
		t.Fatal("joker or map identities changed")
	}
	// The module is registered; sessions start only when its Game document,
	// seeded by migration 0054, enables it.
	registry, err := games.NewRegistry(games.Options{Realtime: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, registered := registry.Tick(houseTanks.GameKey); !registered {
		t.Fatal("house tanks must be registered")
	}
	catalog := gameApplication.NewCatalog(registry, emptyGameConfigs{}, nil)
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if config, err := catalog.Config(houseTanks.GameKey); err != nil || config.Enabled || fixture.LiveGatewayAvailable {
		t.Fatal("without its Game document the game must stay disabled")
	}
}

// emptyGameConfigs is a deployment without any Game document.
type emptyGameConfigs struct{}

func (emptyGameConfigs) List(context.Context) ([]gameAbstract.GameConfigRecord, error) {
	return nil, nil
}

func (emptyGameConfigs) Save(_ context.Context, record gameAbstract.GameConfigRecord, _ int64) (gameAbstract.GameConfigRecord, error) {
	return record, nil
}

func TestHouseTanksClientContractFixtures(t *testing.T) {
	fixture := readHouseTanksFixture[houseTanksProtocolFixture](t, "houseTanksProtocol")
	for _, entry := range fixture.ClientMessages {
		t.Run(entry.Name, func(t *testing.T) {
			decoded, err := realtime.DecodeV2ClientMessage(entry.Message, houseTanks.Module())
			if err != nil {
				t.Fatal(err)
			}
			assertHouseRocketsJSON(t, entry.Message, decoded.Envelope)
			input := decodedTanksInput(decoded)
			switch entry.Name {
			case "ready", "notReady":
				if decoded.Ready == nil || decoded.Ready.Ready != (entry.Name == "ready") {
					t.Fatal("explicit false must remain valid")
				}
			case "press", "release":
				if input == nil || input.Kind != houseTanks.InputPress || input.Pressed != (entry.Name == "press") || input.ControlGeneration != "controlA" || input.ClientTick < 1180 {
					t.Fatalf("press input = %+v", input)
				}
				if entry.Name == "press" && (input.Heading != 0 || input.InputSequence != 42) {
					t.Fatal("zero heading must remain valid")
				}
			case "roundReady", "roundNotReady":
				if input == nil || input.Kind != houseTanks.InputRoundReady || input.Ready != (entry.Name == "roundReady") || input.ControlGeneration != "controlA" {
					t.Fatalf("round ready input = %+v", input)
				}
			case "ping":
				if decoded.Ping == nil || decoded.Ping.PingID != "clock1" {
					t.Fatal("missing ping correlation")
				}
			}
		})
	}
	for _, entry := range fixture.InvalidClientMessages {
		t.Run(entry.Name, func(t *testing.T) {
			_, err := realtime.DecodeV2ClientMessage([]byte(entry.JSON), houseTanks.Module())
			if err == nil {
				t.Fatal("invalid client message was accepted")
			}
			protocolError := realtime.V2ProtocolErrorFrom(houseTanks.GameKey, err)
			if protocolError.Code != entry.ErrorCode || protocolError.Retryable {
				t.Fatalf("unexpected rejection: %+v", protocolError)
			}
		})
	}
}

func TestHouseTanksClientContractBoundaries(t *testing.T) {
	valid := `{"protocolVersion":2,"messageId":"a","type":"houseTanks.press","payload":{"controlGeneration":"g","inputSequence":1,"pressed":true,"clientTick":0,"heading":7.853981633974483}}`
	decoded, err := realtime.DecodeV2ClientMessage([]byte(valid), houseTanks.Module())
	if err != nil || math.Abs(decodedTanksInput(decoded).Heading-math.Pi/2) > 1e-12 {
		t.Fatalf("heading normalization failed: %v", err)
	}
	for _, command := range []string{"gameSession.join", "gameSession.leave", "gameSession.cancel", "houseTanks.resync"} {
		if _, err := realtime.DecodeV2ClientMessage([]byte(`{"protocolVersion":2,"messageId":"a","type":"`+command+`"}`), houseTanks.Module()); err != nil {
			t.Fatal(err)
		}
	}
	invalid := []string{
		strings.Replace(valid, `"controlGeneration":"g"`, `"controlGeneration":"`+strings.Repeat("g", 129)+`"`, 1),
		strings.Replace(valid, `"controlGeneration":"g"`, `"controlGeneration":" g"`, 1),
		strings.Replace(valid, `"inputSequence":1`, `"inputSequence":9223372036854775808`, 1),
		strings.Replace(valid, `"clientTick":0`, `"clientTick":9223372036854775808`, 1),
		strings.Replace(valid, `"heading":7.853981633974483`, `"heading":"NaN"`, 1),
		strings.Replace(valid, `"heading":7.853981633974483`, `"Heading":0`, 1),
		strings.Replace(valid, `"pressed":true`, `"pressed":true,"Pressed":false`, 1),
		strings.Replace(valid, `"pressed":true`, `"pressed":1`, 1),
		`{"protocolVersion":2,"messageId":"a","type":"houseTanks.roundReady","payload":{"controlGeneration":"g","ready":true,"ready":false}}`,
		`{"protocolVersion":2,"messageId":"a","type":"houseTanks.roundReady","payload":{"controlGeneration":" ","ready":true}}`,
		`{"protocolVersion":2,"messageId":"a","type":"houseTanks.roundReady","payload":[]}`,
		`{"protocolVersion":2,"messageId":"a","type":"houseTanks.roundReady"}`,
		`{"protocolVersion":2,"messageId":"a","type":"houseTanks.press"}`,
	}
	for index, message := range invalid {
		if _, err := realtime.DecodeV2ClientMessage([]byte(message), houseTanks.Module()); err == nil {
			t.Fatalf("invalid boundary %d accepted", index)
		}
	}
	for _, heading := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if (houseTanks.HouseTanksPressModel{ControlGeneration: "g", InputSequence: 1, Heading: heading}).Valid() {
			t.Fatal("nonfinite heading accepted")
		}
	}
	// The trusted gateway identity, never the payload, names the player.
	intent, err := houseTanks.Module().DecodeIntent(houseTanks.PressMessageType, json.RawMessage(`{"controlGeneration":"g","inputSequence":3,"pressed":false,"clientTick":5,"heading":-1}`))
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	input := intent.RuntimeInput(module.ControlInput{MessageID: "m", PlayerID: "p", ConnectionID: "c", RuntimeEpoch: 4, CreatedAt: createdAt}).(houseTanks.RuntimeInput)
	expected := houseTanks.RuntimeInput{MessageID: "m", Kind: houseTanks.InputPress, PlayerID: "p", ConnectionID: "c", RuntimeEpoch: 4, ControlGeneration: "g", InputSequence: 3, ClientTick: 5, Heading: -1, CreatedAt: createdAt}
	if input != expected {
		t.Fatalf("runtime input = %+v", input)
	}
}

func TestHouseTanksServerContractFixtures(t *testing.T) {
	fixture := readHouseTanksFixture[houseTanksProtocolFixture](t, "houseTanksProtocol")
	covered := map[string]bool{}
	for _, entry := range fixture.ServerMessages {
		covered[entry.Name] = true
		t.Run(entry.Name, func(t *testing.T) {
			switch entry.Name {
			case "welcome":
				roundTripHouseRocketsMessage[houseTanks.HouseTanksWelcomeModel](t, entry.Message)
				var message realtime.V2ServerMessage[houseTanks.HouseTanksWelcomeModel]
				if err := json.Unmarshal(entry.Message, &message); err != nil {
					t.Fatal(err)
				}
				welcome := houseTanks.Module().Welcome(message.Payload.SessionID, message.Payload.ConnectionID, nil)
				if !reflect.DeepEqual(welcome, message.Payload) {
					t.Fatalf("welcome = %+v", welcome)
				}
			case "pong":
				roundTripHouseRocketsMessage[dtos.RealtimePongModel](t, entry.Message)
			case "accepted":
				roundTripHouseRocketsMessage[struct{}](t, entry.Message)
			case "controlGranted":
				roundTripHouseRocketsMessage[houseTanks.HouseTanksControlGrantedModel](t, entry.Message)
			case "countdown", "roundCountdown", "roundPlaying", "roundReady", "recovering", "ended":
				roundTripHouseRocketsMessage[houseTanks.HouseTanksSnapshotModel](t, entry.Message)
				var message realtime.V2ServerMessage[houseTanks.HouseTanksSnapshotModel]
				if err := json.Unmarshal(entry.Message, &message); err != nil {
					t.Fatal(err)
				}
				if message.Sequence == nil || *message.Sequence != message.Payload.StateSequence {
					t.Fatal("envelope and game sequence differ")
				}
				assertHouseTanksSnapshotPolicy(t, message.Payload, fixture.Rules.EventWindowMilliseconds)
			case "result", "insufficientPlayersResult", "cancelledBeforeStartResult":
				roundTripHouseRocketsMessage[houseTanks.HouseTanksResultModel](t, entry.Message)
				var message realtime.V2ServerMessage[houseTanks.HouseTanksResultModel]
				if err := json.Unmarshal(entry.Message, &message); err != nil {
					t.Fatal(err)
				}
				assertHouseTanksResultPolicy(t, message.Payload)
			case "rejected":
				var message realtime.V2ErrorMessage
				if err := json.Unmarshal(entry.Message, &message); err != nil {
					t.Fatal(err)
				}
				assertHouseRocketsJSON(t, entry.Message, realtime.NewV2ErrorMessage(message.MessageID, message.SentAt, message.Error))
			default:
				t.Fatal("uncovered server fixture")
			}
		})
	}
	for _, phase := range []houseTanks.RoundPhase{houseTanks.RoundCountdown, houseTanks.RoundPlaying, houseTanks.RoundReady} {
		if !covered[string(phase)] {
			t.Fatalf("round phase %s has no fixture", phase)
		}
	}
}

// assertHouseTanksSnapshotPolicy checks the invariants a client may rely on.
func assertHouseTanksSnapshotPolicy(t *testing.T, snapshot houseTanks.HouseTanksSnapshotModel, eventWindowMilliseconds int64) {
	t.Helper()
	if snapshot.GameKey != houseTanks.GameKey || snapshot.MapID == "" || snapshot.MapVersion < 1 || len(snapshot.Seed) != 16 {
		t.Fatal("snapshot must name its game, map and seed")
	}
	if (snapshot.Phase == houseTanks.PhaseCountdown) != (snapshot.RoundPhase == nil) || (snapshot.Phase == houseTanks.PhaseCountdown) != (snapshot.CountdownEndsAt != nil) {
		t.Fatal("only the lobby countdown preview has no round phase")
	}
	if len(snapshot.Players) != len(snapshot.Tanks) || len(snapshot.Players) < houseTanks.MinimumPlayers || len(snapshot.Players) > houseTanks.MaximumPlayers {
		t.Fatal("every seat has exactly one tank")
	}
	colors := houseTanks.PlayerColors()
	generations := 0
	for index, player := range snapshot.Players {
		if player.Seat != index || player.Color != colors[index] || snapshot.Tanks[index].PlayerID != player.PlayerID {
			t.Fatal("players, tanks and colors follow seat order")
		}
		if player.Armor < 0 || player.Armor > houseTanks.ArmorPerRound || player.IsAlive != (player.Armor > 0) || player.RoundsWon > houseTanks.RoundWinsNeeded {
			t.Fatal("armor and round wins out of range")
		}
		if (player.PowerUp == nil) != (player.PowerUpExpiresAtTick == nil) || (player.PowerUpExpiresAtTick != nil && *player.PowerUpExpiresAtTick <= snapshot.Tick) {
			t.Fatal("an active joker has a future expiry")
		}
		if player.HasLeft && player.Connected {
			t.Fatal("a departed player cannot be connected")
		}
		// Only the viewer's own controlling seat carries its generation, and
		// keeps it while its tank is dead between rounds.
		if player.ControlGeneration != nil {
			generations++
			if !player.Connected || generations > 1 || snapshot.Phase != houseTanks.PhasePlaying {
				t.Fatal("a control generation belongs to the viewer's connected seat while playing")
			}
		}
		if direction := snapshot.Tanks[index].RotationDirection; direction != 1 && direction != -1 {
			t.Fatal("rotation direction is a sign")
		}
		if !player.Connected && snapshot.Tanks[index].Controllable {
			t.Fatal("a tank without a connected player is a stationary target")
		}
	}
	if snapshot.RoundPhase != nil {
		switch *snapshot.RoundPhase {
		case houseTanks.RoundCountdown:
			if snapshot.RoundCountdownEndsAt == nil || snapshot.RoundNumber < 1 {
				t.Fatal("round countdown needs its deadline")
			}
		case houseTanks.RoundReady:
			if snapshot.RoundCountdownEndsAt != nil || snapshot.RoundNumber < 1 {
				t.Fatal("round ready follows an ended round")
			}
		case houseTanks.MatchEnded:
			if snapshot.ChampionID == nil && snapshot.Phase == houseTanks.PhaseEnded {
				t.Fatal("a committed match end names the champion")
			}
		}
		if *snapshot.RoundPhase != houseTanks.RoundReady && snapshot.RoundReadyWindowEndsAt != nil {
			t.Fatal("the ready window exists only between rounds")
		}
	}
	if snapshot.ChampionID != nil && snapshot.Phase != houseTanks.PhaseEnded {
		t.Fatal("the champion is announced only after the result commits")
	}
	if snapshot.Token != nil && snapshot.Token.ExpiresAtTick <= snapshot.Tick {
		t.Fatal("an expired token is removed")
	}
	window := eventWindowMilliseconds * houseTanks.PhysicsRateHz / 1000
	for index, event := range snapshot.Events {
		if index > 0 && event.Sequence != snapshot.Events[index-1].Sequence+1 {
			t.Fatal("events are contiguous and ordered")
		}
		if event.Tick > snapshot.Tick || snapshot.Tick-event.Tick > window {
			t.Fatal("event outside the event window")
		}
	}
	for _, projectile := range snapshot.Projectiles {
		if projectile.SpawnTick > snapshot.Tick {
			t.Fatal("projectile from the future")
		}
	}
}

func assertHouseTanksResultPolicy(t *testing.T, result houseTanks.HouseTanksResultModel) {
	t.Helper()
	if result.GameKey != houseTanks.GameKey || result.ProtocolVersion != houseTanks.ProtocolVersion || len(result.Players) < houseTanks.MinimumPlayers {
		t.Fatal("result identity")
	}
	if (result.StartedAt == nil) != (result.MapID == nil) || (result.MapID == nil) != (result.MapVersion == nil) {
		t.Fatal("only a match that never started has no map")
	}
	if result.StartedAt == nil && (result.DurationSeconds != 0 || result.RoundsPlayed != 0) {
		t.Fatal("match cancelled before start has no playing time")
	}
	if result.StartedAt != nil && math.Abs(result.EndedAt.Sub(*result.StartedAt).Seconds()-result.DurationSeconds) > 1e-9 {
		t.Fatal("duration and timestamps differ")
	}
	wins := 0
	for _, player := range result.Players {
		wins += player.RoundsWon
		if player.RoundsWon < 0 || player.RoundsWon > houseTanks.RoundWinsNeeded {
			t.Fatal("round wins out of range")
		}
	}
	if wins > result.RoundsPlayed {
		t.Fatal("more round wins than rounds")
	}
	if result.Status == houseTanks.ResultCancelled {
		if result.ChampionID != nil || result.EndReason == houseTanks.EndChampionDecided {
			t.Fatal("cancelled match has no champion")
		}
		for _, player := range result.Players {
			if player.FinalRank != nil || player.RoundsWon == houseTanks.RoundWinsNeeded {
				t.Fatal("cancelled results award no ranks and no champion score")
			}
		}
		return
	}
	if result.Status != houseTanks.ResultCompleted || result.EndReason != houseTanks.EndChampionDecided || result.ChampionID == nil {
		t.Fatal("only a decided champion completes a match")
	}
	champions := 0
	for _, player := range result.Players {
		rank := 1
		for _, other := range result.Players {
			if other.RoundsWon > player.RoundsWon {
				rank++
			}
		}
		if player.FinalRank == nil || *player.FinalRank != rank {
			t.Fatal("competition ranks by round wins, ties share a rank")
		}
		if player.RoundsWon == houseTanks.RoundWinsNeeded {
			champions++
			if player.PlayerID != *result.ChampionID {
				t.Fatal("the champion reached the needed round wins")
			}
		}
	}
	if champions != 1 {
		t.Fatal("exactly one champion")
	}
}

// Settings change transport, presence and lag compensation only.
func TestHouseTanksConfigBounds(t *testing.T) {
	game := houseTanks.Module()
	rules := game.Definition().Rules
	for name, settings := range map[string]string{
		"snapshotAboveScheduler":    `{"snapshotRateHz":61}`,
		"inputAbovePressLimit":      `{"maximumInputRateHz":40,"maximumPressMessagesPerSecond":30}`,
		"timeoutBelowTwoBeats":      `{"controlHeartbeatMilliseconds":2000,"controlTimeoutMilliseconds":3999}`,
		"negativeGrace":             `{"reconnectGraceMilliseconds":-1}`,
		"lagWindowAboveLimit":       `{"lagCompensationMilliseconds":251}`,
		"negativeHeadingTolerance":  `{"headingToleranceMilliradians":-1}`,
		"contractValue":             `{"physicsRateHz":60}`,
		"matchRulesAreContract":     `{"roundWinsNeeded":5}`,
		"arenaIsContract":           `{"arenaWidth":2000}`,
		"fractionalRate":            `{"snapshotRateHz":20.5}`,
		"trailingValue":             `{"snapshotRateHz":20} {}`,
		"notAnObject":               `[20]`,
		"heartbeatAboveTenSeconds":  `{"controlHeartbeatMilliseconds":10001,"controlTimeoutMilliseconds":30000}`,
		"steerSettingOfOtherGame":   `{"maximumSteerMessagesPerSecond":30}`,
		"lagCompensationFractional": `{"lagCompensationMilliseconds":1.5}`,
	} {
		if _, err := game.ResolveConfig(module.Config{Rules: rules, Settings: json.RawMessage(settings)}); !errors.Is(err, module.ErrInvalidConfig) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	for name, change := range map[string]func(*module.Config){
		"morePlayersThanColors": func(config *module.Config) { config.Rules.MaximumPlayers = houseTanks.MaximumPlayers + 1 },
		"singlePlayer":          func(config *module.Config) { config.Rules.MinimumPlayers = 1 },
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
	resolved, err := game.ResolveConfig(module.Config{Rules: rules, Settings: json.RawMessage(`{"snapshotRateHz":30,"lagCompensationMilliseconds":100}`)})
	if err != nil {
		t.Fatal(err)
	}
	var settings houseTanks.Settings
	if err := json.Unmarshal(resolved.Settings, &settings); err != nil {
		t.Fatal(err)
	}
	expected := houseTanks.DefaultSettings()
	expected.SnapshotRateHz, expected.LagCompensationMilliseconds = 30, 100
	if settings != expected {
		t.Fatalf("settings = %+v, want %+v", settings, expected)
	}
	defaults, err := game.ResolveConfig(module.Config{Rules: rules})
	if err != nil || defaults.Rules != rules {
		t.Fatalf("defaults = %+v %v", defaults, err)
	}
}

// A session's frozen settings reach the welcome and the transport limits; the
// defaults reach every session that has none or no longer resolves.
func TestHouseTanksTimingFollowsSessionSettings(t *testing.T) {
	game := houseTanks.Module()
	frozen := json.RawMessage(`{"snapshotRateHz":10,"maximumInputRateHz":3,"maximumPressMessagesPerSecond":3,"controlHeartbeatMilliseconds":500,"controlTimeoutMilliseconds":1000,"reconnectGraceMilliseconds":2000,"lagCompensationMilliseconds":50,"headingToleranceMilliradians":10}`)
	welcome := game.Welcome("settings-session", "settings-connection", frozen).(houseTanks.HouseTanksWelcomeModel)
	if welcome.Settings.SnapshotRateHz != 10 || welcome.Settings.LagCompensationMilliseconds != 50 || welcome.Settings.PhysicsRateHz != houseTanks.PhysicsRateHz {
		t.Fatalf("welcome settings = %+v", welcome.Settings)
	}
	if timing := game.Timing(frozen); timing.ControlTimeout != time.Second || timing.MaximumIntentRate != 3 || timing.InputLifetime != houseTanks.InputLifetime {
		t.Fatalf("timing = %+v", timing)
	}
	for _, settings := range []json.RawMessage{nil, json.RawMessage(`{"snapshotRateHz":0}`), json.RawMessage(`{"unknown":1}`)} {
		if game.Welcome("s", "c", settings).(houseTanks.HouseTanksWelcomeModel).Settings != houseTanks.HouseTanksRuntimeSettings() {
			t.Fatalf("settings %s did not fall back to defaults", settings)
		}
	}
	if _, err := game.DecodeIntent("houseTanks.unknownIntent", json.RawMessage(`{}`)); !errors.Is(err, module.ErrUnsupportedIntent) {
		t.Fatalf("unknown intent: %v", err)
	}
}

func FuzzHouseTanksClientDecoder(f *testing.F) {
	f.Add([]byte(`{"protocolVersion":2,"messageId":"a","type":"gameSession.join"}`))
	f.Add([]byte(`{"protocolVersion":2,"messageId":"a","type":"houseTanks.press","payload":{"controlGeneration":"g","inputSequence":1,"pressed":true,"clientTick":0,"heading":0}}`))
	f.Add([]byte(`{"protocolVersion":2,"messageId":"a","type":"houseTanks.roundReady","payload":{"controlGeneration":"g","ready":false}}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		decoded, err := realtime.DecodeV2ClientMessage(data, houseTanks.Module())
		if err != nil {
			return
		}
		if decoded.Envelope.ProtocolVersion != 2 || decoded.Envelope.MessageID == "" || len(data) > 16*1024 {
			t.Fatal("invalid envelope accepted")
		}
		input := decodedTanksInput(decoded)
		if input == nil {
			return
		}
		if input.ControlGeneration == "" || input.ClientTick < 0 || math.Abs(input.Heading) > math.Pi || math.IsNaN(input.Heading) {
			t.Fatal("invalid or unnormalized intent accepted")
		}
		if input.Kind == houseTanks.InputPress && input.InputSequence <= 0 {
			t.Fatal("press without a sequence accepted")
		}
	})
}
