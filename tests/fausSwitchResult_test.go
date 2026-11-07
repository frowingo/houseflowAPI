package tests

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games"
	"houseflowApi/internal/application/game/games/fausSwitch"
	"houseflowApi/internal/application/game/module"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/migrations"
	"houseflowApi/internal/models/dtos"
)

// fausSwitchFixtureResult is the fixture's completed result and the stored
// session it belongs to: both players ready before the countdown.
func fausSwitchFixtureResult(t *testing.T) (fausSwitch.FausSwitchResultModel, json.RawMessage, gameDomain.SessionSnapshot) {
	t.Helper()
	fixture := readFausSwitchFixture(t)
	for _, entry := range fixture.ServerMessages {
		if entry.Name != "result" {
			continue
		}
		var message struct {
			Payload json.RawMessage `json:"payload"`
		}
		var result fausSwitch.FausSwitchResultModel
		if json.Unmarshal(entry.Message, &message) != nil || json.Unmarshal(message.Payload, &result) != nil {
			t.Fatal("result fixture")
		}
		startedAt := *result.StartedAt
		session := gameDomain.SessionSnapshot{SessionID: result.SessionID, HouseID: result.HouseID, GameKey: fausSwitch.GameKey, State: gameDomain.SessionFinished,
			Rules: fausSwitch.Definition().Rules, StartedAt: startedAt, UpdatedAt: result.EndedAt}
		for _, player := range result.Players {
			session.Players = append(session.Players, gameDomain.SessionPlayer{PlayerID: player.PlayerID, State: gameDomain.PlayerFinished,
				JoinedAt: startedAt.Add(-time.Minute), ReadyAt: startedAt.Add(-time.Minute)})
		}
		return result, message.Payload, session
	}
	t.Fatal("fixture has no result")
	return fausSwitch.FausSwitchResultModel{}, nil, gameDomain.SessionSnapshot{}
}

func cloneFausSwitchResult(result fausSwitch.FausSwitchResultModel) fausSwitch.FausSwitchResultModel {
	encoded, _ := json.Marshal(result)
	var copied fausSwitch.FausSwitchResultModel
	_ = json.Unmarshal(encoded, &copied)
	return copied
}

func sameJSON(t *testing.T, actual any, expected json.RawMessage) bool {
	t.Helper()
	encoded, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var left, right any
	_ = json.Unmarshal(encoded, &left)
	_ = json.Unmarshal(expected, &right)
	return reflect.DeepEqual(left, right)
}

// The fixture's result is a result this server stores and serves: it
// validates against its session, survives FinalizeResult and comes back from
// storage, value for value, as the fausSwitch.result payload and the HTTP
// result response.
func TestFausSwitchFixtureResultRoundTrips(t *testing.T) {
	result, payload, session := fausSwitchFixtureResult(t)
	if err := fausSwitch.ValidateResult(result, session); err != nil {
		t.Fatal(err)
	}
	game := fausSwitch.Module()
	stored, err := fausSwitch.EncodeMatchResult(result)
	if err != nil {
		t.Fatal(err)
	}
	final, err := game.FinalizeResult(stored, session)
	if err != nil {
		t.Fatal(err)
	}
	served, err := game.DecodeResult(final)
	if err != nil || !sameJSON(t, served, payload) {
		t.Fatalf("served result differs from the fixture: %v", err)
	}
	var response fausSwitchResponse[json.RawMessage]
	if json.Unmarshal(readFausSwitchFixture(t).HTTP.Result.Response, &response) != nil || !sameJSON(t, served, response.Data) {
		t.Fatal("HTTP result differs from the fixture")
	}
}

func TestFausSwitchResultValidationRejectsInconsistentResults(t *testing.T) {
	result, _, session := fausSwitchFixtureResult(t)
	ptr := func(value int64) *int64 { return &value }
	reason := func(value fausSwitch.EliminationReason) *fausSwitch.EliminationReason { return &value }
	rank := func(value int) *int { return &value }
	for name, change := range map[string]func(*fausSwitch.FausSwitchResultModel){
		"session":   func(result *fausSwitch.FausSwitchResultModel) { result.SessionID = "other" },
		"house":     func(result *fausSwitch.FausSwitchResultModel) { result.HouseID = "other" },
		"game":      func(result *fausSwitch.FausSwitchResultModel) { result.GameKey = "houseRockets" },
		"course":    func(result *fausSwitch.FausSwitchResultModel) { result.CourseVersion = 6 },
		"start":     func(result *fausSwitch.FausSwitchResultModel) { result.StartedAt = nil },
		"early end": func(result *fausSwitch.FausSwitchResultModel) { result.EndedAt = result.StartedAt.Add(time.Second) },
		"half tick": func(result *fausSwitch.FausSwitchResultModel) { result.DurationSeconds = 42.001 },
		"too long":  func(result *fausSwitch.FausSwitchResultModel) { result.DurationSeconds = 121 },
		"rank":      func(result *fausSwitch.FausSwitchResultModel) { result.Players[1].Rank = rank(1) },
		"no rank":   func(result *fausSwitch.FausSwitchResultModel) { result.Players[0].Rank = nil },
		"winner":    func(result *fausSwitch.FausSwitchResultModel) { result.WinnerID = &result.Players[1].PlayerID },
		"no winner": func(result *fausSwitch.FausSwitchResultModel) { result.WinnerID = nil },
		"still running": func(result *fausSwitch.FausSwitchResultModel) {
			result.Players[1].EliminatedAtTick, result.Players[1].EliminationReason = nil, nil
		},
		"time limit":      func(result *fausSwitch.FausSwitchResultModel) { result.EndReason = fausSwitch.EndTimeLimit },
		"finish too late": func(result *fausSwitch.FausSwitchResultModel) { result.Players[0].FinishedAtTick = ptr(5041) },
		"two stops":       func(result *fausSwitch.FausSwitchResultModel) { result.Players[0].EliminatedAtTick = ptr(10) },
		"reason alone": func(result *fausSwitch.FausSwitchResultModel) {
			result.Players[0].EliminationReason = reason(fausSwitch.EliminationBoundary)
		},
		"laser": func(result *fausSwitch.FausSwitchResultModel) {
			*result.Players[1].EliminationReason = fausSwitch.EliminationReason(9)
		},
		"distance": func(result *fausSwitch.FausSwitchResultModel) { result.Players[0].Distance = 1e9 },
		"outsider": func(result *fausSwitch.FausSwitchResultModel) {
			result.Players[1].PlayerID = "507f1f77bcf86cd799439099"
		},
		"missing player": func(result *fausSwitch.FausSwitchResultModel) { result.Players = result.Players[:1] },
		"cancel ranked": func(result *fausSwitch.FausSwitchResultModel) {
			result.Status, result.EndReason, result.WinnerID = fausSwitch.ResultCancelled, fausSwitch.EndCancelledByUser, nil
		},
		"cancel reason": func(result *fausSwitch.FausSwitchResultModel) {
			result.Status, result.WinnerID = fausSwitch.ResultCancelled, nil
			result.Players[0].Rank, result.Players[1].Rank = nil, nil
		},
	} {
		changed := cloneFausSwitchResult(result)
		change(&changed)
		if fausSwitch.ValidateResult(changed, session) == nil {
			t.Fatalf("%s: inconsistent result accepted", name)
		}
	}
}

// Client handoff §9.6: when time runs out the robot furthest ahead is ranked
// first alone, yet has not finished, so there is no winner. The server
// accepts it; the Swift validation must too.
func TestFausSwitchTimeLimitLeaderIsNoWinner(t *testing.T) {
	result, _, session := fausSwitchFixtureResult(t)
	limit := float64(fausSwitch.MaximumMatchTicks) / fausSwitch.PhysicsRateHz
	result.EndReason, result.DurationSeconds, result.WinnerID = fausSwitch.EndTimeLimit, limit, nil
	result.EndedAt = result.StartedAt.Add(fausSwitch.MaximumMatchDuration)
	result.Players[0].FinishedAtTick = nil
	if err := fausSwitch.ValidateResult(result, session); err != nil || *result.Players[0].Rank != 1 || *result.Players[1].Rank != 2 {
		t.Fatalf("time-limit leader: %v", err)
	}
	winner := result.Players[0].PlayerID
	result.WinnerID = &winner
	if fausSwitch.ValidateResult(result, session) == nil {
		t.Fatal("an unfinished leader is no winner")
	}
	// Two finishers on the same tick share first place: no winner either.
	result, _, session = fausSwitchFixtureResult(t)
	tied := *result.Players[0].FinishedAtTick
	result.Players[1].EliminatedAtTick, result.Players[1].EliminationReason, result.Players[1].FinishedAtTick = nil, nil, &tied
	*result.Players[1].Rank, result.WinnerID = 1, nil
	if err := fausSwitch.ValidateResult(result, session); err != nil {
		t.Fatalf("shared first place: %v", err)
	}
}

// Without race state a cancellation comes from the frozen roster alone: no
// ranks, finishes, eliminations or distances, and nobody who left before
// the countdown.
func TestFausSwitchCancellationResultComesFromTheFrozenRoster(t *testing.T) {
	_, _, session := fausSwitchFixtureResult(t)
	session.Players = append(session.Players, gameDomain.SessionPlayer{PlayerID: "507f1f77bcf86cd799439030", State: gameDomain.PlayerLeft,
		JoinedAt: session.StartedAt.Add(-time.Hour), ReadyAt: session.StartedAt.Add(-time.Hour), LeftAt: session.StartedAt.Add(-time.Minute)})
	now := session.StartedAt.Add(10 * time.Second)
	stored, err := fausSwitch.Module().CancellationResult(session, string(fausSwitch.EndRecoveryFailed), now)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fausSwitch.DecodeMatchResult(stored)
	if err != nil || result.Status != fausSwitch.ResultCancelled || result.EndReason != fausSwitch.EndRecoveryFailed || result.WinnerID != nil || len(result.Players) != 2 {
		t.Fatalf("cancellation = %+v %v", result, err)
	}
	for _, player := range result.Players {
		if player.Rank != nil || player.FinishedAtTick != nil || player.EliminatedAtTick != nil || player.Distance != 0 {
			t.Fatalf("invented standing %+v", player)
		}
	}
}

var fausSwitchLocalizedCodes = []string{"result_not_found", "result_conflict", "invalid_result", "finalizing", "runtime_completion_required",
	"not_in_lobby", "invalid_input", "runner_finished"}

func TestFausSwitchMigrationsAreRegistered(t *testing.T) {
	versions := map[string]string{}
	for _, migration := range migrations.AllMigrations() {
		if _, taken := versions[migration.Version()]; taken {
			t.Fatalf("version %s is used twice", migration.Version())
		}
		versions[migration.Version()] = migration.Name()
	}
	if versions["0060"] != "fausSwitchLocalizations" || versions["0061"] != "fausSwitchConfig" {
		t.Fatalf("faus switch migrations: %q %q", versions["0060"], versions["0061"])
	}
	if module.ErrorCode(fausSwitch.GameKey, "result_not_found") != fausSwitch.ResultNotFoundErrorCode ||
		module.ErrorCode(fausSwitch.GameKey, "not_in_lobby") != fausSwitch.NotInLobbyErrorCode ||
		module.ErrorCode(fausSwitch.GameKey, "runner_finished") != fausSwitch.RunnerFinishedErrorCode {
		t.Fatal("error codes")
	}
}

func TestFausSwitchLocalizationMigrationIsRepeatable(t *testing.T) {
	fixture := newGameSessionPersistenceFixture(t)
	for _, migration := range migrations.AllMigrations() {
		if migration.Version() == "0060" {
			for range 2 {
				if err := migration.Up(fixture.ctx, fixture.db); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, code := range fausSwitchLocalizedCodes {
		count, err := fixture.db.Collection("localization").CountDocuments(fixture.ctx, map[string]any{"key": module.ErrorCode(fausSwitch.GameKey, code)})
		if err != nil || count != 2 {
			t.Fatalf("%s: %d documents, %v", code, count, err)
		}
	}
}

// Decision K3: Faus Switch is seeded for every house with the code defaults;
// the catalog advertises it with the fixture's settings, and running the
// migration again keeps what an operator changed since.
func TestFausSwitchConfigSeedEnablesEveryHouse(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	registry, err := games.NewRegistry(games.Options{Realtime: true})
	if err != nil {
		t.Fatal(err)
	}
	configs := database.NewGameConfigRepository(fixture.db.Client(), fixture.db.Name())
	catalog := gameApplication.NewCatalog(registry, configs, nil)
	if err := catalog.Refresh(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	config, err := catalog.Config(fausSwitch.GameKey)
	if err != nil {
		t.Fatal(err)
	}
	var seeded fausSwitch.Settings
	_ = json.Unmarshal(config.Settings, &seeded)
	if config.Version != 1 || !config.AvailableTo(fixture.houseID) || len(config.HouseIDs) != 0 || config.Rules != fausSwitch.Definition().Rules || seeded != fausSwitch.DefaultSettings() {
		t.Fatalf("seeded config = %+v %s", config, config.Settings)
	}
	entries, err := gameQueries.NewGetGameCatalogHandler(catalog, fixture.membershipPolicy()).Handle(fixture.ctx,
		gameQueries.GetGameCatalogQuery{HouseID: fixture.houseID, UserID: fixture.memberID})
	if err != nil {
		t.Fatal(err)
	}
	advertised := false
	for _, entry := range entries {
		if entry.Definition.GameKey != fausSwitch.GameKey {
			continue
		}
		advertised = true
		var model struct {
			Settings json.RawMessage `json:"settings"`
		}
		encoded, _ := json.Marshal(dtos.GameCatalogEntryToModel(entry.Definition, entry.Config, entry.ClientSettings))
		if json.Unmarshal(encoded, &model) != nil || !sameJSON(t, model.Settings, readFausSwitchFixture(t).Settings) {
			t.Fatalf("catalog settings = %s", model.Settings)
		}
	}
	if !advertised {
		t.Fatal("faus switch is not in the house's catalog")
	}
	if _, err := configs.Save(fixture.ctx, gameAbstract.GameConfigRecord{GameKey: fausSwitch.GameKey, Enabled: false, HouseIDs: []string{fixture.houseID}}, 1); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations.AllMigrations() {
		if migration.Version() == "0061" {
			if err := migration.Up(fixture.ctx, fixture.db); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := catalog.Refresh(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if config, _ := catalog.Config(fausSwitch.GameKey); config.Version != 2 || config.Enabled || len(config.HouseIDs) != 1 {
		t.Fatalf("the rerun overwrote the operator's config: %+v", config)
	}
}

// Without its Game document the registered game stays switched off.
func TestFausSwitchStaysDisabledWithoutItsGameDocument(t *testing.T) {
	registry, err := games.NewRegistry(games.Options{Realtime: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, registered := registry.Tick(fausSwitch.GameKey); !registered {
		t.Fatal("faus switch must be registered")
	}
	catalog := gameApplication.NewCatalog(registry, emptyGameConfigs{}, nil)
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if config, err := catalog.Config(fausSwitch.GameKey); err != nil || config.Enabled {
		t.Fatal("without its Game document the game must stay disabled")
	}
}
