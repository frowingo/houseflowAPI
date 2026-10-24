package tests

import (
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"testing"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games/houseTanks"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/data/migrations"
)

// runningTanksSession starts a session whose players are frozen in order.
func runningTanksSession(t *testing.T, ids ...string) gameDomain.SessionSnapshot {
	t.Helper()
	definition := houseTanks.Definition()
	now := time.Now().UTC().Truncate(time.Millisecond)
	rules := definition.Rules
	// The ready window holds the session until every player readied.
	rules.ReadyWindowDuration, rules.CountdownDuration = time.Second, 0
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "tanks-result-session", HouseID: "tanks-result-house", GameKey: definition.GameKey,
		ProtocolVersion: definition.ProtocolVersion, Mode: definition.Mode, Rules: rules, CreatedBy: ids[0], CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := session.JoinPlayer(id, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		if err := session.SetReady(id, true, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Advance(now.Add(rules.ReadyWindowDuration)); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.State != gameDomain.SessionRunning || len(snapshot.Players) != len(ids) {
		t.Fatalf("session state = %s with %d players", snapshot.State, len(snapshot.Players))
	}
	return snapshot
}

// tanksSessionDriver runs a bound runtime for the stored session.
func tanksSessionDriver(t *testing.T, snapshot gameDomain.SessionSnapshot) *tanksDriver {
	t.Helper()
	session := houseTanks.RuntimeSession{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, StartedAt: snapshot.StartedAt}
	for _, player := range snapshot.Players {
		session.Players = append(session.Players, houseTanks.RuntimePlayer{PlayerID: player.PlayerID, DisplayName: "Player " + player.PlayerID})
	}
	runtime, err := houseTanks.NewRuntime(session, 4, snapshot.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	driver := attachTanksDriver(t, runtime, 4, snapshot.StartedAt)
	for _, player := range session.Players {
		driver.bind(player.PlayerID, "connection-"+player.PlayerID)
	}
	return driver
}

// playWinners plays one round per entry; an empty winner is a draw. Players
// who left do not vote.
func (driver *tanksDriver) playWinners(winners ...string) {
	driver.t.Helper()
	for _, winner := range winners {
		driver.untilRoundPhase(houseTanks.RoundPlaying)
		driver.destroyAllBut(winner)
		driver.ticks(1)
		if len(driver.results) > 0 {
			return
		}
		for _, player := range driver.match().Players {
			if !player.HasLeft {
				if err := driver.ready(player.PlayerID, true); err != nil {
					driver.t.Fatal(err)
				}
			}
		}
	}
}

func (driver *tanksDriver) onlyResult() houseTanks.HouseTanksResultModel {
	driver.t.Helper()
	for range 4 {
		if len(driver.results) > 0 {
			break
		}
		driver.ticks(1)
	}
	if len(driver.results) != 1 {
		driver.t.Fatalf("%d results proposed", len(driver.results))
	}
	return driver.results[0]
}

func ranksOf(result houseTanks.HouseTanksResultModel) []any {
	ranks := make([]any, 0, len(result.Players))
	for _, player := range result.Players {
		if player.FinalRank == nil {
			ranks = append(ranks, nil)
		} else {
			ranks = append(ranks, *player.FinalRank)
		}
	}
	return ranks
}

func cloneTanksResult(result houseTanks.HouseTanksResultModel) houseTanks.HouseTanksResultModel {
	encoded, _ := json.Marshal(result)
	var copied houseTanks.HouseTanksResultModel
	_ = json.Unmarshal(encoded, &copied)
	return copied
}

func TestHouseTanksResultRanksShareTies(t *testing.T) {
	snapshot := runningTanksSession(t, "a", "b", "c", "d")
	driver := tanksSessionDriver(t, snapshot)
	driver.playWinners("a", "b", "", "c", "a", "a")
	result := driver.onlyResult()
	if result.Status != houseTanks.ResultCompleted || result.EndReason != houseTanks.EndChampionDecided || result.ChampionID == nil || *result.ChampionID != "a" ||
		result.RoundsPlayed != 6 {
		t.Fatalf("result = %+v", result)
	}
	if ranks := ranksOf(result); !reflect.DeepEqual(ranks, []any{1, 2, 2, 4}) {
		t.Fatalf("ranks = %v, want 1 2 2 4", ranks)
	}
	if err := houseTanks.ValidateResult(result, snapshot); err != nil {
		t.Fatal(err)
	}
	// The wire shape is the fixture's.
	fixture := readHouseTanksFixture[houseTanksProtocolFixture](t, "houseTanksProtocol")
	for _, entry := range fixture.ServerMessages {
		if entry.Name != "result" {
			continue
		}
		var message struct {
			Payload map[string]any `json:"payload"`
		}
		_ = json.Unmarshal(entry.Message, &message)
		var actual map[string]any
		encoded, _ := json.Marshal(result)
		_ = json.Unmarshal(encoded, &actual)
		if !reflect.DeepEqual(sortedKeys(message.Payload), sortedKeys(actual)) ||
			!reflect.DeepEqual(sortedKeys(message.Payload["players"].([]any)[0]), sortedKeys(actual["players"].([]any)[0])) {
			t.Fatalf("result keys = %v, want %v", sortedKeys(actual), sortedKeys(message.Payload))
		}
	}
}

func sortedKeys(value any) []string {
	object, _ := value.(map[string]any)
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestHouseTanksCancelledResultsKeepScores(t *testing.T) {
	t.Run("cancelledByUser", func(t *testing.T) {
		snapshot := runningTanksSession(t, "a", "b", "c")
		driver := tanksSessionDriver(t, snapshot)
		driver.playWinners("a", "b")
		if err := driver.runtime.Cancel(houseTanks.EndCancelledByUser); err != nil {
			t.Fatal(err)
		}
		result := driver.onlyResult()
		if result.Status != houseTanks.ResultCancelled || result.EndReason != houseTanks.EndCancelledByUser || result.ChampionID != nil || result.RoundsPlayed != 2 ||
			result.Players[0].RoundsWon != 1 || result.Players[1].RoundsWon != 1 || !reflect.DeepEqual(ranksOf(result), []any{nil, nil, nil}) {
			t.Fatalf("result = %+v", result)
		}
		if err := houseTanks.ValidateResult(result, snapshot); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("insufficientPlayers", func(t *testing.T) {
		snapshot := runningTanksSession(t, "a", "b", "c")
		driver := tanksSessionDriver(t, snapshot)
		driver.playWinners("a", "a")
		driver.runtime.Reconcile([]string{"a"}, []string{"a", "b", "c"})
		result := driver.onlyResult()
		if result.Status != houseTanks.ResultCancelled || result.EndReason != houseTanks.EndInsufficientPlayers || result.Players[0].RoundsWon != 2 || result.ChampionID != nil {
			t.Fatalf("result = %+v", result)
		}
		if err := houseTanks.ValidateResult(result, snapshot); err != nil {
			t.Fatal(err)
		}
	})
}

func TestHouseTanksResultValidationRejectsInconsistentResults(t *testing.T) {
	snapshot := runningTanksSession(t, "a", "b", "c", "d")
	driver := tanksSessionDriver(t, snapshot)
	driver.playWinners("a", "b", "", "c", "a", "a")
	completed := driver.onlyResult()
	rank := func(value int) *int { return &value }
	text := func(value string) *string { return &value }
	cases := map[string]func(*houseTanks.HouseTanksResultModel){
		"session":  func(result *houseTanks.HouseTanksResultModel) { result.SessionID = "other-session" },
		"house":    func(result *houseTanks.HouseTanksResultModel) { result.HouseID = "other-house" },
		"gameKey":  func(result *houseTanks.HouseTanksResultModel) { result.GameKey = "houseRockets" },
		"protocol": func(result *houseTanks.HouseTanksResultModel) { result.ProtocolVersion = 1 },
		"nilStart": func(result *houseTanks.HouseTanksResultModel) { result.StartedAt = nil },
		"wrongStart": func(result *houseTanks.HouseTanksResultModel) {
			value := snapshot.StartedAt.Add(time.Second)
			result.StartedAt = &value
		},
		"earlyEnd":    func(result *houseTanks.HouseTanksResultModel) { result.EndedAt = snapshot.StartedAt },
		"partialTick": func(result *houseTanks.HouseTanksResultModel) { result.DurationSeconds += 0.001 },
		"nan":         func(result *houseTanks.HouseTanksResultModel) { result.DurationSeconds = math.NaN() },
		"tooLong": func(result *houseTanks.HouseTanksResultModel) {
			result.DurationSeconds = 300 + 1.0/houseTanks.PhysicsRateHz
		},
		"otherMap":       func(result *houseTanks.HouseTanksResultModel) { other := houseTanks.MapBasement; result.MapID = &other },
		"noMap":          func(result *houseTanks.HouseTanksResultModel) { result.MapID, result.MapVersion = nil, nil },
		"halfMap":        func(result *houseTanks.HouseTanksResultModel) { result.MapVersion = nil },
		"duplicate":      func(result *houseTanks.HouseTanksResultModel) { result.Players[1].PlayerID = "a" },
		"outsider":       func(result *houseTanks.HouseTanksResultModel) { result.Players[3].PlayerID = "outsider" },
		"missingPlayer":  func(result *houseTanks.HouseTanksResultModel) { result.Players = result.Players[:3] },
		"negativeWins":   func(result *houseTanks.HouseTanksResultModel) { result.Players[3].RoundsWon = -1 },
		"fourWins":       func(result *houseTanks.HouseTanksResultModel) { result.Players[0].RoundsWon = 4 },
		"winsOverRounds": func(result *houseTanks.HouseTanksResultModel) { result.RoundsPlayed = 4 },
		"roundsBound":    func(result *houseTanks.HouseTanksResultModel) { result.RoundsPlayed = 10000 },
		"twoChampions":   func(result *houseTanks.HouseTanksResultModel) { result.Players[1].RoundsWon = 3 },
		"wrongChampion":  func(result *houseTanks.HouseTanksResultModel) { result.ChampionID = text("b") },
		"noChampion":     func(result *houseTanks.HouseTanksResultModel) { result.ChampionID = nil },
		"wrongReason":    func(result *houseTanks.HouseTanksResultModel) { result.EndReason = houseTanks.EndSessionExpired },
		"untiedRanks":    func(result *houseTanks.HouseTanksResultModel) { result.Players[2].FinalRank = rank(3) },
		"missingRank":    func(result *houseTanks.HouseTanksResultModel) { result.Players[3].FinalRank = nil },
		"unknownStatus":  func(result *houseTanks.HouseTanksResultModel) { result.Status = "abandoned" },
		"cancelledWithChampion": func(result *houseTanks.HouseTanksResultModel) {
			result.Status, result.EndReason = houseTanks.ResultCancelled, houseTanks.EndCancelledByUser
		},
		"cancelledWithRanks": func(result *houseTanks.HouseTanksResultModel) {
			result.Status, result.EndReason, result.ChampionID, result.Players[0].RoundsWon = houseTanks.ResultCancelled, houseTanks.EndCancelledByUser, nil, 2
		},
		"cancelledChampionReason": func(result *houseTanks.HouseTanksResultModel) {
			result.Status, result.ChampionID, result.Players[0].RoundsWon = houseTanks.ResultCancelled, nil, 2
			for index := range result.Players {
				result.Players[index].FinalRank = nil
			}
		},
	}
	// The clean cases must pass, or the mutations prove nothing.
	if err := houseTanks.ValidateResult(completed, snapshot); err != nil {
		t.Fatal(err)
	}
	cancelled := cloneTanksResult(completed)
	cases["cancelledWithRanks"](&cancelled)
	for index := range cancelled.Players {
		cancelled.Players[index].FinalRank = nil
	}
	if err := houseTanks.ValidateResult(cancelled, snapshot); err != nil {
		t.Fatalf("a cancelled result keeps its scores: %v", err)
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			result := cloneTanksResult(completed)
			mutate(&result)
			if houseTanks.ValidateResult(result, snapshot) == nil {
				t.Fatal("inconsistent result accepted")
			}
		})
	}
}

func TestHouseTanksCancellationResultComesFromTheFrozenRoster(t *testing.T) {
	snapshot := runningTanksSession(t, "a", "b", "c")
	// Only players frozen into the match count: one left before the start and
	// one never readied; one who left during the match still counts.
	snapshot.Players = append(snapshot.Players,
		gameDomain.SessionPlayer{PlayerID: "early", State: gameDomain.PlayerLeft, JoinedAt: snapshot.StartedAt, ReadyAt: snapshot.StartedAt, LeftAt: snapshot.StartedAt.Add(-time.Second)},
		gameDomain.SessionPlayer{PlayerID: "idle", State: gameDomain.PlayerLeft, JoinedAt: snapshot.StartedAt, LeftAt: snapshot.StartedAt.Add(time.Second)},
		gameDomain.SessionPlayer{PlayerID: "late", State: gameDomain.PlayerLeft, JoinedAt: snapshot.StartedAt, ReadyAt: snapshot.StartedAt, LeftAt: snapshot.StartedAt.Add(time.Minute)})
	game := houseTanks.Module()
	stored, err := game.CancellationResult(snapshot, string(houseTanks.EndRecoveryFailed), snapshot.StartedAt.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != gameDomain.MatchCancelled || stored.EndReason != string(houseTanks.EndRecoveryFailed) || stored.SchemaVersion != houseTanks.ResultSchemaVersion {
		t.Fatalf("stored = %+v", stored)
	}
	decoded, err := game.DecodeResult(stored)
	if err != nil {
		t.Fatal(err)
	}
	result := decoded.(houseTanks.HouseTanksResultModel)
	ids := []string{}
	for _, player := range result.Players {
		if player.RoundsWon != 0 || player.FinalRank != nil {
			t.Fatal("a lost match invents no score")
		}
		ids = append(ids, player.PlayerID)
	}
	expectedMap, _ := houseTanks.MapByID(*result.MapID)
	if !reflect.DeepEqual(ids, []string{"a", "b", "c", "late"}) || result.ChampionID != nil || result.RoundsPlayed != 0 || *result.MapVersion != expectedMap.Version {
		t.Fatalf("result = %+v", result)
	}
	if _, err := game.FinalizeResult(stored, snapshot); err != nil {
		t.Fatalf("a cancellation result must finalize: %v", err)
	}
	if _, err := game.CancellationResult(snapshot, string(houseTanks.EndChampionDecided), snapshot.StartedAt.Add(time.Minute)); err == nil {
		t.Fatal("a cancellation cannot decide a champion")
	}
}

func TestHouseTanksFinalizeResultAnchorsToTheSession(t *testing.T) {
	snapshot := runningTanksSession(t, "a", "b")
	driver := tanksSessionDriver(t, snapshot)
	driver.playWinners("a", "a", "a")
	proposed := driver.onlyResult()
	game := houseTanks.Module()
	stored, err := houseTanks.EncodeMatchResult(proposed)
	if err != nil {
		t.Fatal(err)
	}
	// The session row was updated after the runtime ended: the end follows it.
	snapshot.UpdatedAt = proposed.EndedAt.Add(time.Minute)
	finalized, err := game.FinalizeResult(stored, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	result, err := houseTanks.DecodeMatchResult(finalized)
	if err != nil {
		t.Fatal(err)
	}
	if !result.StartedAt.Equal(snapshot.StartedAt) || !result.EndedAt.Equal(snapshot.UpdatedAt) || !finalized.EndedAt.Equal(snapshot.UpdatedAt) || finalized.Status != gameDomain.MatchCompleted {
		t.Fatalf("finalized = %+v", result)
	}
	// Stored rows that disagree with their payload are refused.
	for name, mutate := range map[string]func(*gameDomain.MatchResult){
		"schema":  func(row *gameDomain.MatchResult) { row.SchemaVersion = houseTanks.ResultSchemaVersion + 1 },
		"gameKey": func(row *gameDomain.MatchResult) { row.GameKey = "houseRockets" },
		"status":  func(row *gameDomain.MatchResult) { row.Status = gameDomain.MatchCancelled },
		"reason":  func(row *gameDomain.MatchResult) { row.EndReason = string(houseTanks.EndSessionExpired) },
		"endedAt": func(row *gameDomain.MatchResult) { row.EndedAt = row.EndedAt.Add(time.Second) },
		"payload": func(row *gameDomain.MatchResult) { row.Payload = json.RawMessage(`{"sessionId":"other"}`) },
	} {
		row := finalized
		mutate(&row)
		if _, err := houseTanks.DecodeMatchResult(row); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	// A result for another roster never finalizes.
	other := runningTanksSession(t, "a", "c")
	if _, err := game.FinalizeResult(stored, other); err == nil {
		t.Fatal("result finalized against another roster")
	}
}

func TestHouseTanksLocalizationsCoverResultErrors(t *testing.T) {
	registered := false
	for _, migration := range migrations.AllMigrations() {
		registered = registered || (migration.Version() == "0053" && migration.Name() == "houseTanksLocalizations")
	}
	if !registered {
		t.Fatal("migration 0053 is not registered")
	}
	if module.ErrorCode(houseTanks.GameKey, "result_not_found") != houseTanks.ResultNotFoundErrorCode {
		t.Fatal("result_not_found code")
	}
}

func TestHouseTanksLocalizationMigrationIsRepeatable(t *testing.T) {
	fixture := newGameSessionPersistenceFixture(t)
	for _, migration := range migrations.AllMigrations() {
		if migration.Version() == "0053" {
			for range 2 {
				if err := migration.Up(fixture.ctx, fixture.db); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, code := range []string{"result_not_found", "result_conflict", "invalid_result", "finalizing", "runtime_completion_required"} {
		count, err := fixture.db.Collection("localization").CountDocuments(fixture.ctx, map[string]any{"key": module.ErrorCode(houseTanks.GameKey, code)})
		if err != nil || count != 2 {
			t.Fatalf("%s: %d documents, %v", code, count, err)
		}
	}
}
