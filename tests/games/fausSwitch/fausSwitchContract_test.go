package fausSwitch

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games"
	"houseflowApi/internal/application/game/games/fausSwitch"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit"
	"houseflowApi/tests/testkit/gamekit"
)

type fausSwitchFixtureMessage struct {
	Name    string          `json:"name"`
	Message json.RawMessage `json:"message"`
}

type fausSwitchProtocolFixture struct {
	SchemaVersion    int    `json:"schemaVersion"`
	ContractRevision string `json:"contractRevision"`
	ProtocolVersion  int    `json:"protocolVersion"`
	CourseVersion    int    `json:"courseVersion"`
	Rules            struct {
		MinimumPlayers          int   `json:"minimumPlayers"`
		MaximumPlayers          int   `json:"maximumPlayers"`
		ReadyWindowMilliseconds int64 `json:"readyWindowMilliseconds"`
		CountdownMilliseconds   int64 `json:"countdownMilliseconds"`
		fausSwitch.FausSwitchRuntimeSettingsModel
		CrowdedRunnerThreshold int     `json:"crowdedRunnerThreshold"`
		CrowdedRunnerScale     float64 `json:"crowdedRunnerScale"`
	} `json:"rules"`
	Settings       json.RawMessage            `json:"settings"`
	PlayerColors   []fausSwitch.Color         `json:"playerColors"`
	ClientMessages []fausSwitchFixtureMessage `json:"clientMessages"`
	ServerMessages []fausSwitchFixtureMessage `json:"serverMessages"`
	HTTP           struct {
		Ensure struct {
			Method   string          `json:"method"`
			Path     string          `json:"path"`
			Request  json.RawMessage `json:"request"`
			Response json.RawMessage `json:"response"`
		} `json:"ensure"`
		Result struct {
			Method         string          `json:"method"`
			Path           string          `json:"path"`
			Response       json.RawMessage `json:"response"`
			NotFoundStatus int             `json:"notFoundStatus"`
		} `json:"result"`
	} `json:"http"`
	ErrorCodes []string `json:"errorCodes"`
}

// fausSwitchResponse is the shared HTTP success envelope.
type fausSwitchResponse[T any] struct {
	Success bool `json:"success"`
	Data    T    `json:"data"`
}

func readFausSwitchFixture(t *testing.T) fausSwitchProtocolFixture {
	t.Helper()
	data, err := os.ReadFile(testkit.RepoPath("internal/application/game/games/fausSwitch/fixtures/fausSwitchProtocol.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture fausSwitchProtocolFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestFausSwitchDefinitionContract(t *testing.T) {
	fixture := readFausSwitchFixture(t)
	definition := fausSwitch.Definition()
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	if fixture.SchemaVersion != 1 || fixture.ContractRevision != fausSwitch.ContractRevision ||
		fixture.ProtocolVersion != definition.ProtocolVersion || realtime.V2ProtocolVersion != definition.ProtocolVersion ||
		fixture.CourseVersion != fausSwitch.CourseVersion || fausSwitch.CourseVersion != 7 {
		t.Fatal("fixture revisions and backend definition differ")
	}
	rules := fixture.Rules
	if definition.GameKey != "fausSwitch" || definition.Rules.MinimumPlayers != 2 || definition.Rules.MaximumPlayers != 8 ||
		rules.MinimumPlayers != definition.Rules.MinimumPlayers || rules.MaximumPlayers != definition.Rules.MaximumPlayers ||
		rules.ReadyWindowMilliseconds != definition.Rules.ReadyWindowDuration.Milliseconds() ||
		rules.CountdownMilliseconds != definition.Rules.CountdownDuration.Milliseconds() {
		t.Fatal("player or lobby policy changed")
	}
	defaults := fausSwitch.RuntimeSettings(nil)
	if rules.FausSwitchRuntimeSettingsModel != defaults {
		t.Fatalf("rules settings = %+v, want %+v", rules.FausSwitchRuntimeSettingsModel, defaults)
	}
	gamekit.AssertHouseRocketsJSON(t, fixture.Settings, defaults)
	if rules.CrowdedRunnerThreshold != fausSwitch.CrowdedRunnerThreshold || rules.CrowdedRunnerScale != fausSwitch.CrowdedRunnerScale {
		t.Fatal("crowded race rule changed")
	}
	// The tick limits are the millisecond contract at the physics rate.
	if defaults.PhysicsRateHz != fausSwitch.PhysicsRateHz ||
		defaults.MaximumRewindMilliseconds*fausSwitch.PhysicsRateHz/1000 != fausSwitch.MaximumRewindTicks ||
		defaults.MaximumMatchMilliseconds*fausSwitch.PhysicsRateHz/1000 != fausSwitch.MaximumMatchTicks ||
		fausSwitch.PhysicsRateHz%fausSwitch.SchedulingRateHz != 0 {
		t.Fatal("tick limits and millisecond contract differ")
	}
	if !reflect.DeepEqual(fixture.PlayerColors, fausSwitch.PlayerColors()) || len(fixture.PlayerColors) != fausSwitch.MaximumPlayers {
		t.Fatal("eight stable colors are required in catalog order")
	}
	for _, color := range fixture.PlayerColors {
		if !color.Valid() {
			t.Fatalf("color %s is not valid", color)
		}
	}
	colors := fausSwitch.PlayerColors()
	colors[0] = fausSwitch.ColorBerry
	if fausSwitch.PlayerColors()[0] != fausSwitch.ColorPlum || fausSwitch.Color("laser").Valid() {
		t.Fatal("colors share mutable storage or accept unknown values")
	}
	gameCodes := []string{fausSwitch.InvalidInputErrorCode, fausSwitch.ControlUnavailableErrorCode, fausSwitch.StaleControlErrorCode,
		fausSwitch.RunnerFinishedErrorCode, fausSwitch.NotRunningErrorCode, fausSwitch.NotInLobbyErrorCode, fausSwitch.ResultNotFoundErrorCode}
	for _, code := range gameCodes {
		if !strings.HasPrefix(code, fausSwitch.GameKey+".error.") || module.ErrorCode(fausSwitch.GameKey, strings.TrimPrefix(code, fausSwitch.GameKey+".error.")) != code {
			t.Fatalf("error code %s is not namespaced", code)
		}
	}
	shared := []string{"realtime.error.invalid_command", "realtime.error.rate_limited", "realtime.error.unavailable", "realtime.error.command_failed"}
	if !reflect.DeepEqual(fixture.ErrorCodes, append(gameCodes, shared...)) {
		t.Fatalf("error codes = %v", fixture.ErrorCodes)
	}
	if module.ResyncMessageType(fausSwitch.GameKey) != fausSwitch.ResyncMessageType ||
		module.ResultMessageType(fausSwitch.GameKey) != fausSwitch.ResultMessageType ||
		module.ControlGrantedMessageType(fausSwitch.GameKey) != fausSwitch.ControlGrantedMessageType {
		t.Fatal("shared message types and module constants differ")
	}
}

func TestFausSwitchClientContractFixtures(t *testing.T) {
	fixture := readFausSwitchFixture(t)
	covered := map[string]bool{}
	for _, entry := range fixture.ClientMessages {
		covered[entry.Name] = true
		t.Run(entry.Name, func(t *testing.T) {
			switch entry.Name {
			case "join", "ready", "ping":
				decoded, err := realtime.DecodeV2ClientMessage(entry.Message, nil)
				if err != nil {
					t.Fatal(err)
				}
				gamekit.AssertHouseRocketsJSON(t, entry.Message, decoded.Envelope)
				if entry.Name == "ready" && (decoded.Ready == nil || !decoded.Ready.Ready) {
					t.Fatal("ready payload lost")
				}
				if entry.Name == "ping" && (decoded.Ping == nil || decoded.Ping.PingID != "clock1") {
					t.Fatal("missing ping correlation")
				}
			case "resync":
				envelope := decodeFausSwitchEnvelope(t, entry.Message)
				if envelope.Type != fausSwitch.ResyncMessageType || string(envelope.Payload) != "{}" {
					t.Fatalf("resync = %+v", envelope)
				}
			case "selectColor":
				envelope := decodeFausSwitchEnvelope(t, entry.Message)
				selection, err := fausSwitch.DecodeSelectColor(envelope.Payload)
				if err != nil || envelope.Type != fausSwitch.SelectColorMessageType || selection.Color != fausSwitch.ColorOcean {
					t.Fatalf("select color = %+v %v", selection, err)
				}
				assertFausSwitchEnvelope(t, entry.Message, envelope, selection)
			case "flip":
				envelope := decodeFausSwitchEnvelope(t, entry.Message)
				flip, err := fausSwitch.DecodeFlip(envelope.Payload)
				expected := fausSwitch.FausSwitchFlipModel{ControlGeneration: "controlA", InputSequence: 1, ClientTick: 118}
				if err != nil || envelope.Type != fausSwitch.FlipMessageType || flip != expected {
					t.Fatalf("flip = %+v %v", flip, err)
				}
				assertFausSwitchEnvelope(t, entry.Message, envelope, flip)
			default:
				t.Fatal("uncovered client fixture")
			}
		})
	}
	for _, name := range []string{"join", "ready", "selectColor", "flip", "resync", "ping"} {
		if !covered[name] {
			t.Fatalf("client message %s has no fixture", name)
		}
	}
}

func decodeFausSwitchEnvelope(t *testing.T, data json.RawMessage) realtime.ClientMessage {
	t.Helper()
	var envelope realtime.ClientMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.ProtocolVersion != fausSwitch.ProtocolVersion || envelope.MessageID == "" {
		t.Fatalf("envelope = %+v", envelope)
	}
	return envelope
}

// assertFausSwitchEnvelope re-encodes the decoded payload in its envelope:
// nothing the client sends may be lost or renamed.
func assertFausSwitchEnvelope(t *testing.T, expected json.RawMessage, envelope realtime.ClientMessage, payload any) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Payload = encoded
	gamekit.AssertHouseRocketsJSON(t, expected, envelope)
}

func TestFausSwitchClientPayloadBoundaries(t *testing.T) {
	valid := `{"controlGeneration":"g","inputSequence":1,"clientTick":0}`
	if flip, err := fausSwitch.DecodeFlip(json.RawMessage(valid)); err != nil || flip.ClientTick != 0 {
		t.Fatalf("tick 0 must stay valid: %v", err)
	}
	invalidFlips := []string{
		strings.Replace(valid, `"controlGeneration":"g"`, `"controlGeneration":"`+strings.Repeat("g", 129)+`"`, 1),
		strings.Replace(valid, `"controlGeneration":"g"`, `"controlGeneration":" g"`, 1),
		strings.Replace(valid, `"controlGeneration":"g"`, `"controlGeneration":""`, 1),
		strings.Replace(valid, `"inputSequence":1`, `"inputSequence":0`, 1),
		strings.Replace(valid, `"inputSequence":1`, `"inputSequence":9223372036854775808`, 1),
		strings.Replace(valid, `"inputSequence":1`, `"inputSequence":1.5`, 1),
		strings.Replace(valid, `"clientTick":0`, `"clientTick":-1`, 1),
		strings.Replace(valid, `"clientTick":0`, `"clientTick":"0"`, 1),
		strings.Replace(valid, `"clientTick":0`, `"ClientTick":0`, 1),
		strings.Replace(valid, `"clientTick":0`, `"clientTick":0,"clientTick":1`, 1),
		strings.Replace(valid, `"clientTick":0`, `"clientTick":0,"color":"plum"`, 1),
		`{"controlGeneration":"g","inputSequence":1}`,
		`[]`, `null`, ``,
	}
	for index, payload := range invalidFlips {
		if _, err := fausSwitch.DecodeFlip(json.RawMessage(payload)); !errors.Is(err, fausSwitch.ErrInvalidInput) {
			t.Errorf("invalid flip %d accepted: %v", index, err)
		}
	}
	for _, color := range fausSwitch.PlayerColors() {
		if selection, err := fausSwitch.DecodeSelectColor(json.RawMessage(`{"color":"` + string(color) + `"}`)); err != nil || selection.Color != color {
			t.Fatalf("color %s rejected: %v", color, err)
		}
	}
	for index, payload := range []string{`{"color":"Plum"}`, `{"color":"red"}`, `{"color":""}`, `{"color":null}`, `{"color":1}`,
		`{"color":"plum","color":"ocean"}`, `{"color":"plum","seat":0}`, `{}`, `[]`, `null`} {
		if _, err := fausSwitch.DecodeSelectColor(json.RawMessage(payload)); !errors.Is(err, fausSwitch.ErrInvalidInput) {
			t.Errorf("invalid color %d accepted: %v", index, err)
		}
	}
}

func TestFausSwitchServerContractFixtures(t *testing.T) {
	fixture := readFausSwitchFixture(t)
	covered := map[string]bool{}
	for _, entry := range fixture.ServerMessages {
		covered[entry.Name] = true
		t.Run(entry.Name, func(t *testing.T) {
			switch entry.Name {
			case "welcome":
				gamekit.RoundTripHouseRocketsMessage[fausSwitch.FausSwitchWelcomeModel](t, entry.Message)
				var message realtime.V2ServerMessage[fausSwitch.FausSwitchWelcomeModel]
				if err := json.Unmarshal(entry.Message, &message); err != nil {
					t.Fatal(err)
				}
				if welcome := fausSwitch.Welcome(message.Payload.SessionID, message.Payload.ConnectionID, nil); welcome != message.Payload {
					t.Fatalf("welcome = %+v", welcome)
				}
			case "pong":
				gamekit.RoundTripHouseRocketsMessage[dtos.RealtimePongModel](t, entry.Message)
			case "accepted":
				gamekit.RoundTripHouseRocketsMessage[struct{}](t, entry.Message)
			case "sessionSnapshot":
				gamekit.RoundTripHouseRocketsMessage[dtos.GameSessionResponseModel](t, entry.Message)
			case "controlGranted":
				gamekit.RoundTripHouseRocketsMessage[fausSwitch.FausSwitchControlGrantedModel](t, entry.Message)
			case "countdown", "crowdedCountdown", "playing":
				gamekit.RoundTripHouseRocketsMessage[fausSwitch.FausSwitchSnapshotModel](t, entry.Message)
				var message realtime.V2ServerMessage[fausSwitch.FausSwitchSnapshotModel]
				if err := json.Unmarshal(entry.Message, &message); err != nil {
					t.Fatal(err)
				}
				if message.Type != fausSwitch.SnapshotMessageType || message.Sequence == nil || *message.Sequence != message.Payload.StateSequence {
					t.Fatal("snapshot type or envelope sequence differ")
				}
				assertFausSwitchSnapshotPolicy(t, message.Payload)
			case "result", "cancelledResult":
				gamekit.RoundTripHouseRocketsMessage[fausSwitch.FausSwitchResultModel](t, entry.Message)
				var message realtime.V2ServerMessage[fausSwitch.FausSwitchResultModel]
				if err := json.Unmarshal(entry.Message, &message); err != nil {
					t.Fatal(err)
				}
				assertFausSwitchResultPolicy(t, message.Payload)
			case "rejected":
				var message realtime.V2ErrorMessage
				if err := json.Unmarshal(entry.Message, &message); err != nil {
					t.Fatal(err)
				}
				gamekit.AssertHouseRocketsJSON(t, entry.Message, realtime.NewV2ErrorMessage(message.MessageID, message.SentAt, message.Error))
				if !slices.Contains(fixture.ErrorCodes, message.Error.Code) {
					t.Fatalf("unknown error code %s", message.Error.Code)
				}
			default:
				t.Fatal("uncovered server fixture")
			}
		})
	}
	for _, name := range []string{"welcome", "sessionSnapshot", "controlGranted", "countdown", "crowdedCountdown", "playing", "result", "cancelledResult", "rejected"} {
		if !covered[name] {
			t.Fatalf("server message %s has no fixture", name)
		}
	}
}

// The fixture's sessionSnapshot carries the lobby state the server itself
// writes (ADR-030): replaying each joined player's color claim through the
// module's ApplyLobbyIntent yields the fixture's lobbyState exactly.
func TestFausSwitchSessionSnapshotLobbyState(t *testing.T) {
	fixture := readFausSwitchFixture(t)
	index := slices.IndexFunc(fixture.ServerMessages, func(entry fausSwitchFixtureMessage) bool { return entry.Name == "sessionSnapshot" })
	if index < 0 {
		t.Fatal("no sessionSnapshot fixture")
	}
	var message realtime.V2ServerMessage[dtos.GameSessionResponseModel]
	if err := json.Unmarshal(fixture.ServerMessages[index].Message, &message); err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Colors map[string]string `json:"colors"`
	}
	if err := json.Unmarshal(message.Payload.LobbyState, &claims); err != nil || len(claims.Colors) == 0 {
		t.Fatalf("lobbyState %s: %v", message.Payload.LobbyState, err)
	}
	session := gameDomain.SessionSnapshot{SessionID: message.Payload.SessionId, GameKey: message.Payload.GameKey}
	for _, player := range message.Payload.Players {
		session.Players = append(session.Players, gameDomain.SessionPlayer{PlayerID: player.PlayerId, State: gameDomain.PlayerState(player.State)})
	}
	for _, player := range session.Players {
		color, claimed := claims.Colors[player.PlayerID]
		if !claimed {
			continue
		}
		if player.State == gameDomain.PlayerLeft {
			t.Fatalf("player %s left but keeps %s", player.PlayerID, color)
		}
		intent, _ := json.Marshal(map[string]string{"color": color})
		state, err := fausSwitch.ApplyLobbyIntent(session, player.PlayerID, fausSwitch.SelectColorMessageType, intent)
		if err != nil {
			t.Fatal(err)
		}
		session.LobbyState = state
	}
	gamekit.AssertHouseRocketsJSON(t, message.Payload.LobbyState, json.RawMessage(session.LobbyState))
}

// The start grids of the fixture's countdowns are the briefing's control
// tables (§4.2, §4.2.2): seat → gravity and spawn y at x = 380.
func TestFausSwitchCountdownFixturesMatchStartTables(t *testing.T) {
	fixture := readFausSwitchFixture(t)
	expected := map[string][]float64{
		"countdown":        {114.1, 275.8},
		"crowdedCountdown": {51.2, 338.7, 93.1, 296.8, 135.1, 254.8, 177.0, 212.9},
	}
	for _, entry := range fixture.ServerMessages {
		spawns, ok := expected[entry.Name]
		if !ok {
			continue
		}
		var message realtime.V2ServerMessage[fausSwitch.FausSwitchSnapshotModel]
		if err := json.Unmarshal(entry.Message, &message); err != nil {
			t.Fatal(err)
		}
		snapshot := message.Payload
		if snapshot.Phase != fausSwitch.PhaseCountdown || snapshot.Tick != 0 || len(snapshot.Runners) != len(spawns) {
			t.Fatalf("%s is not a tick 0 countdown of %d robots", entry.Name, len(spawns))
		}
		for seat, runner := range snapshot.Runners {
			gravity := fausSwitch.GravityDown
			if seat%2 == 1 {
				gravity = fausSwitch.GravityUp
			}
			if runner.X != 380 || runner.Y != spawns[seat] || runner.Gravity != gravity || runner.VelocityY != 0 ||
				len(runner.UsedBoosts) != 0 || len(runner.UsedSlows) != 0 || runner.LastProcessedInputSequence != 0 {
				t.Fatalf("%s seat %d = %+v", entry.Name, seat, runner)
			}
		}
		delete(expected, entry.Name)
	}
	if len(expected) != 0 {
		t.Fatalf("missing countdown fixtures: %v", expected)
	}
}

func TestFausSwitchHTTPContract(t *testing.T) {
	fixture := readFausSwitchFixture(t)
	ensure := fixture.HTTP.Ensure
	if ensure.Method != "PUT" || ensure.Path != "/api/v1/game/"+fausSwitch.GameKey+"/session" {
		t.Fatalf("ensure route = %s %s", ensure.Method, ensure.Path)
	}
	gamekit.AssertHouseRocketsJSON(t, ensure.Request, dtos.EnsureGameSessionModel{HouseId: "507f1f77bcf86cd799439012"})
	var session fausSwitchResponse[dtos.GameSessionResponseModel]
	if err := json.Unmarshal(ensure.Response, &session); err != nil {
		t.Fatal(err)
	}
	gamekit.AssertHouseRocketsJSON(t, ensure.Response, session)
	if !session.Success || session.Data.GameKey != fausSwitch.GameKey || session.Data.ProtocolVersion != fausSwitch.ProtocolVersion ||
		session.Data.Rules.MaximumPlayers != fausSwitch.MaximumPlayers {
		t.Fatalf("ensure response = %+v", session.Data)
	}
	result := fixture.HTTP.Result
	if result.Method != "GET" || !strings.HasPrefix(result.Path, "/api/v1/game/sessions/") || !strings.HasSuffix(result.Path, "/result") ||
		result.NotFoundStatus != 404 {
		t.Fatalf("result route = %s %s %d", result.Method, result.Path, result.NotFoundStatus)
	}
	var stored fausSwitchResponse[fausSwitch.FausSwitchResultModel]
	if err := json.Unmarshal(result.Response, &stored); err != nil {
		t.Fatal(err)
	}
	gamekit.AssertHouseRocketsJSON(t, result.Response, stored)
	assertFausSwitchResultPolicy(t, stored.Data)
}

// assertFausSwitchSnapshotPolicy checks what the client relies on: it steps
// every robot from these fields, so they must be complete and consistent
// (FausSwitchRealtime.checkCompatibility, FausSwitchRunnerDTO.isValid).
func assertFausSwitchSnapshotPolicy(t *testing.T, snapshot fausSwitch.FausSwitchSnapshotModel) {
	t.Helper()
	if snapshot.GameKey != fausSwitch.GameKey || snapshot.CourseVersion != fausSwitch.CourseVersion || snapshot.SessionID == "" {
		t.Fatal("snapshot must name its game and course")
	}
	count := len(snapshot.Runners)
	if count < fausSwitch.MinimumPlayers || count > fausSwitch.MaximumPlayers {
		t.Fatalf("%d runners", count)
	}
	// The roster is frozen at the countdown: a robot that leaves keeps its
	// seat, so the scale always follows the number of runners.
	scale := 1.0
	if count > fausSwitch.CrowdedRunnerThreshold {
		scale = fausSwitch.CrowdedRunnerScale
	}
	if snapshot.RunnerScale != scale {
		t.Fatalf("runnerScale %v for %d runners", snapshot.RunnerScale, count)
	}
	if snapshot.Tick < 0 || snapshot.ElapsedSeconds != float64(snapshot.Tick)/fausSwitch.PhysicsRateHz {
		t.Fatal("elapsed seconds follow the tick")
	}
	if (snapshot.Phase == fausSwitch.PhaseCountdown) != (snapshot.CountdownEndsAt != nil) ||
		(snapshot.Phase == fausSwitch.PhaseCountdown && snapshot.Tick != 0) {
		t.Fatal("only the tick 0 countdown preview has a countdown deadline")
	}
	if snapshot.WinnerID != nil && snapshot.Phase != fausSwitch.PhaseEnded {
		t.Fatal("the winner is announced only after the result commits")
	}
	players, colors := map[string]bool{}, map[fausSwitch.Color]bool{}
	generations := 0
	for _, runner := range snapshot.Runners {
		if runner.PlayerID == "" || players[runner.PlayerID] || !runner.Color.Valid() || colors[runner.Color] {
			t.Fatal("every seat has its own player and color")
		}
		players[runner.PlayerID], colors[runner.Color] = true, true
		for _, value := range []float64{runner.X, runner.Y, runner.VelocityY} {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatal("physical state must be finite")
			}
		}
		if runner.Gravity != fausSwitch.GravityDown && runner.Gravity != fausSwitch.GravityUp {
			t.Fatalf("gravity %q", runner.Gravity)
		}
		if runner.BoostedUntilTick < 0 || runner.SlowedUntilTick < 0 || runner.LastProcessedInputSequence < 0 {
			t.Fatal("negative tick or sequence")
		}
		for _, pads := range [][]int{runner.UsedBoosts, runner.UsedSlows} {
			if pads == nil || !slices.IsSorted(pads) || len(slices.Compact(slices.Clone(pads))) != len(pads) {
				t.Fatal("used pads are ascending unique ids, never null")
			}
		}
		finished := runner.Status == fausSwitch.RunnerFinished
		crashed := runner.Status == fausSwitch.RunnerCrashed
		if runner.Status != fausSwitch.RunnerRunning && !finished && !crashed {
			t.Fatalf("status %q", runner.Status)
		}
		if finished != (runner.FinishedAtTick != nil) || crashed != (runner.EliminatedAtTick != nil) || crashed != (runner.EliminationReason != nil) {
			t.Fatal("finish and elimination fields follow the status")
		}
		if runner.ControlGeneration != nil {
			generations++
			if generations > 1 || !runner.Connected || snapshot.Phase != fausSwitch.PhasePlaying {
				t.Fatal("a control generation belongs to the viewer's connected robot while playing")
			}
		}
	}
}

// assertFausSwitchResultPolicy checks briefing §4.5 and §7: competition ranks
// (finishers by tick, then runners by distance, then crashes, later first),
// and the winner is the sole rank-one finisher.
func assertFausSwitchResultPolicy(t *testing.T, result fausSwitch.FausSwitchResultModel) {
	t.Helper()
	if result.GameKey != fausSwitch.GameKey || result.ProtocolVersion != fausSwitch.ProtocolVersion ||
		result.CourseVersion != fausSwitch.CourseVersion || len(result.Players) == 0 || len(result.Players) > fausSwitch.MaximumPlayers {
		t.Fatal("result identity")
	}
	if result.StartedAt == nil && result.DurationSeconds != 0 {
		t.Fatal("a race that never started has no duration")
	}
	if result.StartedAt != nil && math.Abs(result.EndedAt.Sub(*result.StartedAt).Seconds()-result.DurationSeconds) > 1e-9 {
		t.Fatal("duration and timestamps differ")
	}
	players := map[string]bool{}
	for _, player := range result.Players {
		if player.PlayerID == "" || players[player.PlayerID] || math.IsNaN(player.Distance) || player.Distance < 0 {
			t.Fatal("players are unique with a finite distance")
		}
		players[player.PlayerID] = true
		if (player.EliminatedAtTick != nil) != (player.EliminationReason != nil) || (player.FinishedAtTick != nil && player.EliminatedAtTick != nil) {
			t.Fatal("a robot either finished, crashed or ran on")
		}
	}
	if result.Status == fausSwitch.ResultCancelled {
		if result.WinnerID != nil || result.EndReason.Completes() {
			t.Fatal("a cancelled race has no winner and no completing end reason")
		}
		for _, player := range result.Players {
			if player.Rank != nil {
				t.Fatal("a cancelled race awards no ranks")
			}
		}
		return
	}
	if result.Status != fausSwitch.ResultCompleted || result.StartedAt == nil || !result.EndReason.Completes() {
		t.Fatal("only raceComplete and timeLimit complete a started race")
	}
	type standing struct {
		group int
		value float64
	}
	key := func(player fausSwitch.FausSwitchPlayerResultModel) standing {
		switch {
		case player.FinishedAtTick != nil:
			return standing{0, float64(*player.FinishedAtTick)}
		case player.EliminatedAtTick != nil:
			return standing{2, -float64(*player.EliminatedAtTick)}
		default:
			return standing{1, -player.Distance}
		}
	}
	less := func(a, b standing) bool { return a.group < b.group || (a.group == b.group && a.value < b.value) }
	firsts := []fausSwitch.FausSwitchPlayerResultModel{}
	for _, player := range result.Players {
		rank := 1
		for _, other := range result.Players {
			if less(key(other), key(player)) {
				rank++
			}
		}
		if player.Rank == nil || *player.Rank != rank {
			t.Fatalf("player %s rank %v, want %d", player.PlayerID, player.Rank, rank)
		}
		if rank == 1 {
			firsts = append(firsts, player)
		}
	}
	soleFinisher := len(firsts) == 1 && firsts[0].FinishedAtTick != nil
	if soleFinisher != (result.WinnerID != nil) || (soleFinisher && *result.WinnerID != firsts[0].PlayerID) {
		t.Fatal("the winner is the sole rank-one finisher, otherwise null")
	}
}

// Settings change transport and presence only; physics, rewind and match
// length are contract values.
func TestFausSwitchConfigBounds(t *testing.T) {
	rules := fausSwitch.Definition().Rules
	for name, settings := range map[string]string{
		"snapshotAboveScheduler":   `{"snapshotRateHz":61}`,
		"snapshotTooSlow":          `{"snapshotRateHz":4}`,
		"inputAboveScheduler":      `{"maximumInputRateHz":61}`,
		"noInput":                  `{"maximumInputRateHz":0}`,
		"timeoutBelowTwoBeats":     `{"controlHeartbeatMilliseconds":2000,"controlTimeoutMilliseconds":3999}`,
		"heartbeatAboveTenSeconds": `{"controlHeartbeatMilliseconds":10001,"controlTimeoutMilliseconds":30000}`,
		"negativeGrace":            `{"reconnectGraceMilliseconds":-1}`,
		"physicsIsContract":        `{"physicsRateHz":60}`,
		"rewindIsContract":         `{"maximumRewindMilliseconds":500}`,
		"matchLengthIsContract":    `{"maximumMatchMilliseconds":60000}`,
		"settingOfOtherGame":       `{"lagCompensationMilliseconds":100}`,
		"fractionalRate":           `{"snapshotRateHz":20.5}`,
		"trailingValue":            `{"snapshotRateHz":20} {}`,
		"notAnObject":              `[20]`,
	} {
		if _, err := fausSwitch.ResolveConfig(module.Config{Rules: rules, Settings: json.RawMessage(settings)}); !errors.Is(err, module.ErrInvalidConfig) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	for name, change := range map[string]func(*module.Config){
		"morePlayersThanColors": func(config *module.Config) { config.Rules.MaximumPlayers = fausSwitch.MaximumPlayers + 1 },
		"singlePlayer":          func(config *module.Config) { config.Rules.MinimumPlayers = 1 },
		"noCountdown":           func(config *module.Config) { config.Rules.CountdownDuration = 0 },
		"longReadyWindow":       func(config *module.Config) { config.Rules.ReadyWindowDuration = 3 * time.Minute },
		"subMillisecond":        func(config *module.Config) { config.Rules.CountdownDuration = 2*time.Second + time.Microsecond },
	} {
		config := module.Config{Rules: rules}
		change(&config)
		if _, err := fausSwitch.ResolveConfig(config); !errors.Is(err, module.ErrInvalidConfig) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	resolved, err := fausSwitch.ResolveConfig(module.Config{Rules: rules, Settings: json.RawMessage(`{"snapshotRateHz":30,"reconnectGraceMilliseconds":0}`)})
	if err != nil {
		t.Fatal(err)
	}
	var settings fausSwitch.Settings
	if err := json.Unmarshal(resolved.Settings, &settings); err != nil {
		t.Fatal(err)
	}
	expected := fausSwitch.DefaultSettings()
	expected.SnapshotRateHz, expected.ReconnectGraceMilliseconds = 30, 0
	if settings != expected || resolved.Rules != rules {
		t.Fatalf("settings = %+v, want %+v", settings, expected)
	}
	if defaults, err := fausSwitch.ResolveConfig(module.Config{Rules: rules}); err != nil || defaults.Rules != rules {
		t.Fatalf("defaults = %+v %v", defaults, err)
	}
}

// A session's frozen settings reach the welcome; the defaults reach every
// session that has none or whose settings no longer resolve.
func TestFausSwitchWelcomeFollowsSessionSettings(t *testing.T) {
	frozen := json.RawMessage(`{"snapshotRateHz":10,"maximumInputRateHz":5,"controlHeartbeatMilliseconds":500,"controlTimeoutMilliseconds":1000,"reconnectGraceMilliseconds":2000}`)
	welcome := fausSwitch.Welcome("session", "connection", frozen)
	expected := fausSwitch.FausSwitchRuntimeSettingsModel{PhysicsRateHz: 120, SnapshotRateHz: 10, MaximumInputRateHz: 5,
		MaximumRewindMilliseconds: 250, ControlHeartbeatMilliseconds: 500, ControlTimeoutMilliseconds: 1000,
		ReconnectGraceMilliseconds: 2000, MaximumMatchMilliseconds: 120_000}
	if welcome.Settings != expected || welcome.CourseVersion != fausSwitch.CourseVersion || welcome.GameKey != fausSwitch.GameKey {
		t.Fatalf("welcome = %+v", welcome)
	}
	for _, settings := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{"snapshotRateHz":0}`), json.RawMessage(`{"unknown":1}`)} {
		if fausSwitch.Welcome("s", "c", settings).Settings != fausSwitch.RuntimeSettings(nil) {
			t.Fatalf("settings %s did not fall back to the defaults", settings)
		}
	}
}

func FuzzFausSwitchFlipDecoder(f *testing.F) {
	f.Add([]byte(`{"controlGeneration":"controlA","inputSequence":1,"clientTick":118}`))
	f.Add([]byte(`{"controlGeneration":"g","inputSequence":9223372036854775807,"clientTick":0}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		flip, err := fausSwitch.DecodeFlip(data)
		if err != nil {
			return
		}
		if !flip.Valid() || flip.ControlGeneration == "" || flip.InputSequence <= 0 || flip.ClientTick < 0 {
			t.Fatalf("invalid flip accepted: %+v", flip)
		}
	})
}

func fausSwitchFixtureSnapshot(t *testing.T, fixture fausSwitchProtocolFixture, name string) (fausSwitch.FausSwitchSnapshotModel, map[string]any) {
	t.Helper()
	for _, entry := range fixture.ServerMessages {
		if entry.Name != name {
			continue
		}
		var typed realtime.V2ServerMessage[fausSwitch.FausSwitchSnapshotModel]
		var loose realtime.V2ServerMessage[map[string]any]
		if json.Unmarshal(entry.Message, &typed) != nil || json.Unmarshal(entry.Message, &loose) != nil {
			t.Fatalf("fixture %s does not decode", name)
		}
		return typed.Payload, loose.Payload
	}
	t.Fatalf("fixture has no %s", name)
	return fausSwitch.FausSwitchSnapshotModel{}, nil
}

func renderFausSwitchFrame(t *testing.T, payload json.RawMessage, viewer module.Viewer) map[string]any {
	t.Helper()
	view, err := fausSwitch.Module().PrepareFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := view.Render(viewer)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(rendered, &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// The countdown snapshots the client was written against are the module's
// preview, value for value: start grid, scale, final colors (the lobby's
// claim first, then catalog order) and every null.
func TestFausSwitchPreviewIsTheCountdownFixture(t *testing.T) {
	fixture := readFausSwitchFixture(t)
	game := fausSwitch.Module()
	for _, name := range []string{"countdown", "crowdedCountdown"} {
		typed, expected := fausSwitchFixtureSnapshot(t, fixture, name)
		players := make([]module.Player, len(typed.Runners))
		claims := map[string]fausSwitch.Color{}
		free := fausSwitch.PlayerColors()
		for seat, runner := range typed.Runners {
			players[seat] = module.Player{PlayerID: runner.PlayerID, DisplayName: runner.DisplayName}
			if runner.Color != free[0] {
				claims[runner.PlayerID] = runner.Color
			}
			free = slices.DeleteFunc(free, func(color fausSwitch.Color) bool { return color == runner.Color })
		}
		lobbyState, _ := json.Marshal(map[string]any{"colors": claims})
		frame, err := game.PreviewFrame(module.PreviewParams{SessionID: typed.SessionID, HouseID: "507f1f77bcf86cd799439012", CountdownEndsAt: *typed.CountdownEndsAt,
			Players: players, Epoch: typed.RuntimeEpoch, Sequence: typed.StateSequence, LobbyState: lobbyState})
		if err != nil {
			t.Fatal(err)
		}
		actual := renderFausSwitchFrame(t, frame.Payload, module.Viewer{PlayerID: players[0].PlayerID})
		if !reflect.DeepEqual(actual, expected) {
			encoded, _ := json.Marshal(actual)
			t.Fatalf("%s preview differs from the fixture:\n%s", name, encoded)
		}
	}
}

// A running race renders the fixture's playing snapshot shape, with the
// generation only on the controlling connection's own robot.
func TestFausSwitchPlayingSnapshotShape(t *testing.T) {
	fixture := readFausSwitchFixture(t)
	_, expected := fausSwitchFixtureSnapshot(t, fixture, "playing")
	game := fausSwitch.Module()
	startedAt := time.Now().UTC()
	runtime, err := game.NewRuntime(module.RuntimeParams{SessionID: "session", HouseID: "house", StartedAt: startedAt, Epoch: 7, Now: startedAt,
		Players: []module.Player{{PlayerID: "a", DisplayName: "A"}, {PlayerID: "b", DisplayName: "B"}}})
	if err != nil {
		t.Fatal(err)
	}
	bind, _ := json.Marshal(fausSwitch.RuntimeInput{Kind: fausSwitch.InputBind, PlayerID: "a", ConnectionID: "connection-a", RuntimeEpoch: 7, CreatedAt: startedAt})
	if err := runtime.Submit(bind, startedAt); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Advance(startedAt.Add(time.Second / fausSwitch.SchedulingRateHz)); err != nil {
		t.Fatal(err)
	}
	var grant *module.ControlGrant
	var payload json.RawMessage
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for grant == nil || payload == nil {
		update, err := runtime.NextUpdate(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if update.Grant != nil {
			grant = update.Grant
		}
		// The bind's frame replaced the first one in the one-frame mailbox.
		if update.Frame != nil {
			payload = update.Frame.Payload
		}
	}
	actual := renderFausSwitchFrame(t, payload, module.Viewer{PlayerID: "a", ConnectionID: "connection-a", Grant: grant})
	keys := func(value any) []string {
		object, _ := value.(map[string]any)
		names := make([]string, 0, len(object))
		for name := range object {
			names = append(names, name)
		}
		slices.Sort(names)
		return names
	}
	if !reflect.DeepEqual(keys(actual), keys(expected)) {
		t.Fatalf("snapshot keys = %v, want %v", keys(actual), keys(expected))
	}
	runners := actual["runners"].([]any)
	if !reflect.DeepEqual(keys(runners[0]), keys(expected["runners"].([]any)[0])) {
		t.Fatalf("runner keys = %v", keys(runners[0]))
	}
	if actual["phase"] != "playing" || runners[0].(map[string]any)["controlGeneration"] != grant.ControlGeneration || runners[1].(map[string]any)["controlGeneration"] != nil {
		t.Fatal("generation")
	}
}

// The gateway reads the client fixture's messages through the registered
// module: selectColor is a lobby intent, flip a gameplay intent.
func TestFausSwitchGatewayDecodesClientFixtures(t *testing.T) {
	fixture := readFausSwitchFixture(t)
	registry, err := games.NewRegistry(games.Options{Realtime: true})
	if err != nil {
		t.Fatal(err)
	}
	game, ok := registry.Game(fausSwitch.GameKey)
	if !ok {
		t.Fatal("fausSwitch is not registered")
	}
	if _, ok := registry.Lobby(fausSwitch.GameKey); !ok {
		t.Fatal("fausSwitch has no lobby intents")
	}
	for _, entry := range fixture.ClientMessages {
		decoded, err := realtime.DecodeV2ClientMessage(entry.Message, game)
		if err != nil {
			t.Fatalf("%s: %v", entry.Name, err)
		}
		switch entry.Name {
		case "selectColor":
			if string(decoded.LobbyIntent) != `{"color":"ocean"}` || decoded.Intent != nil {
				t.Fatalf("selectColor: %+v", decoded)
			}
		case "flip":
			input, _ := json.Marshal(decoded.Intent.RuntimeInput(module.ControlInput{MessageID: "flip1", PlayerID: "a", ConnectionID: "c", RuntimeEpoch: 7}))
			var flip fausSwitch.RuntimeInput
			if json.Unmarshal(input, &flip) != nil || flip.Kind != fausSwitch.InputFlip || flip.ClientTick != 118 || flip.InputSequence != 1 || flip.ControlGeneration != "controlA" {
				t.Fatalf("flip: %s", input)
			}
		}
	}
}

// Every runtime rejection reaches the client as a code of the contract.
func TestFausSwitchRuntimeErrorCodes(t *testing.T) {
	fixture := readFausSwitchFixture(t)
	for err, code := range map[error]string{
		fausSwitch.ErrRunnerFinished:  fausSwitch.RunnerFinishedErrorCode,
		fausSwitch.ErrControlRequired: fausSwitch.StaleControlErrorCode,
		fausSwitch.ErrStaleInput:      fausSwitch.StaleControlErrorCode,
		fausSwitch.ErrRuntimeFinished: fausSwitch.NotRunningErrorCode,
		fausSwitch.ErrPlayerNotFound:  fausSwitch.ControlUnavailableErrorCode,
		fausSwitch.ErrInvalidInput:    fausSwitch.InvalidInputErrorCode,
		fausSwitch.ErrInputExpired:    fausSwitch.InvalidInputErrorCode,
		fausSwitch.ErrInputRate:       "realtime.error.rate_limited",
		fausSwitch.ErrRuntimeBusy:     "realtime.error.unavailable",
	} {
		mapped := realtime.V2ProtocolErrorFrom(fausSwitch.GameKey, err)
		if mapped.Code != code || !slices.Contains(fixture.ErrorCodes, mapped.Code) {
			t.Fatalf("%v → %s, want %s", err, mapped.Code, code)
		}
	}
}
