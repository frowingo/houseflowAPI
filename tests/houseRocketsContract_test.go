package tests

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/gameSpesific/houseRockets"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/core"
	"houseflowApi/internal/models/dtos"
)

type houseRocketsProtocolFixture struct {
	SchemaVersion        int    `json:"schemaVersion"`
	ContractRevision     string `json:"contractRevision"`
	ProtocolVersion      int    `json:"protocolVersion"`
	CourseVersion        int    `json:"courseVersion"`
	LiveGatewayAvailable bool   `json:"liveGatewayAvailable"`
	Rules                struct {
		MinimumPlayers          int   `json:"minimumPlayers"`
		MaximumPlayers          int   `json:"maximumPlayers"`
		ReadyWindowMilliseconds int64 `json:"readyWindowMilliseconds"`
		CountdownMilliseconds   int64 `json:"countdownMilliseconds"`
		houseRockets.HouseRocketsRuntimeSettingsModel
		ExpiryEndReason houseRockets.EndReason `json:"expiryEndReason"`
		ExpiryHasWinner bool                   `json:"expiryHasWinner"`
	} `json:"rules"`
	PlayerColors   []houseRockets.Color `json:"playerColors"`
	ClientMessages []struct {
		Name    string          `json:"name"`
		Message json.RawMessage `json:"message"`
	} `json:"clientMessages"`
	ServerMessages []struct {
		Name    string          `json:"name"`
		Message json.RawMessage `json:"message"`
	} `json:"serverMessages"`
	HTTP struct {
		Ensure struct {
			Request  dtos.EnsureGameSessionModel `json:"request"`
			Response json.RawMessage             `json:"response"`
		} `json:"ensure"`
		Discover struct {
			Response json.RawMessage `json:"response"`
		} `json:"discover"`
		Result struct {
			Response json.RawMessage `json:"response"`
		} `json:"result"`
	} `json:"http"`
	InvalidClientMessages []struct {
		Name      string `json:"name"`
		JSON      string `json:"json"`
		ErrorCode string `json:"errorCode"`
	} `json:"invalidClientMessages"`
}

func readHouseRocketsFixture[T any](t *testing.T, name string) T {
	t.Helper()
	data, err := os.ReadFile("../internal/application/game/gameSpesific/houseRockets/fixtures/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture T
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func assertHouseRocketsJSON(t *testing.T, expected json.RawMessage, actual any) {
	t.Helper()
	encoded, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var expectedJSON, actualJSON any
	if err := json.Unmarshal(expected, &expectedJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &actualJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expectedJSON, actualJSON) {
		t.Fatalf("wire shape changed\nexpected: %s\nactual: %s", expected, encoded)
	}
}

func roundTripHouseRocketsMessage[T any](t *testing.T, data json.RawMessage) {
	t.Helper()
	var message realtime.V2ServerMessage[T]
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatal(err)
	}
	assertHouseRocketsJSON(t, data, realtime.NewV2ServerMessage(message.Type, message.MessageID, message.Sequence, message.SentAt, message.Payload))
}

func TestHouseRocketsDefinitionContract(t *testing.T) {
	fixture := readHouseRocketsFixture[houseRocketsProtocolFixture](t, "houseRocketsProtocol")
	definition := houseRockets.Definition()
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	if fixture.SchemaVersion != 1 || fixture.ContractRevision != houseRockets.ContractRevision ||
		fixture.ProtocolVersion != definition.ProtocolVersion || fixture.CourseVersion != houseRockets.CourseVersion {
		t.Fatal("fixture revisions and backend definition differ")
	}
	if definition.GameKey != "houseRockets" || definition.Rules.MinimumPlayers != 2 || definition.Rules.MaximumPlayers != 8 ||
		fixture.Rules.MinimumPlayers != definition.Rules.MinimumPlayers || fixture.Rules.MaximumPlayers != definition.Rules.MaximumPlayers ||
		fixture.Rules.ReadyWindowMilliseconds != definition.Rules.ReadyWindowDuration.Milliseconds() ||
		fixture.Rules.CountdownMilliseconds != definition.Rules.CountdownDuration.Milliseconds() {
		t.Fatal("player or lobby policy changed")
	}
	if !reflect.DeepEqual(fixture.Rules.HouseRocketsRuntimeSettingsModel, houseRockets.HouseRocketsRuntimeSettings()) ||
		houseRockets.MaximumMatchDuration != 5*time.Minute || fixture.Rules.ExpiryHasWinner ||
		fixture.Rules.ExpiryEndReason != houseRockets.EndSessionExpired {
		t.Fatal("runtime settings or five-minute cancellation policy changed")
	}
	if !reflect.DeepEqual(fixture.PlayerColors, houseRockets.PlayerColors()) || len(fixture.PlayerColors) != 8 {
		t.Fatal("eight stable color identities are required")
	}
	colors := houseRockets.PlayerColors()
	colors[0] = houseRockets.ColorTeal
	if houseRockets.PlayerColors()[0] != houseRockets.ColorMint {
		t.Fatal("colors share mutable storage")
	}
	seen := make(map[houseRockets.Color]bool)
	for _, color := range fixture.PlayerColors {
		if seen[color] {
			t.Fatal("duplicate player color")
		}
		seen[color] = true
	}
	catalog, err := game.NewDefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Find(houseRockets.GameKey); !errors.Is(err, gameAbstract.ErrGameDefinitionNotFound) || fixture.LiveGatewayAvailable {
		t.Fatal("contract-only game must not be advertised as playable")
	}
	flappyBird, err := catalog.Find(game.FlappyBirdGameKey)
	if err != nil || flappyBird.ProtocolVersion != 1 || realtime.ProtocolVersion != 1 {
		t.Fatal("existing v1 catalog/protocol changed")
	}
}

func TestHouseRocketsClientContractFixtures(t *testing.T) {
	fixture := readHouseRocketsFixture[houseRocketsProtocolFixture](t, "houseRocketsProtocol")
	for _, entry := range fixture.ClientMessages {
		t.Run(entry.Name, func(t *testing.T) {
			decoded, err := realtime.DecodeV2ClientMessage(entry.Message)
			if err != nil {
				t.Fatal(err)
			}
			assertHouseRocketsJSON(t, entry.Message, decoded.Envelope)
			switch entry.Name {
			case "ready", "notReady":
				if decoded.Ready == nil || decoded.Ready.Ready != (entry.Name == "ready") {
					t.Fatal("explicit false must remain valid")
				}
			case "steer":
				if decoded.Steer == nil || !decoded.Steer.Valid() || decoded.Steer.Heading != 0 || decoded.Steer.InputSequence != 42 {
					t.Fatal("zero heading must remain valid")
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
			_, err := realtime.DecodeV2ClientMessage([]byte(entry.JSON))
			if err == nil {
				t.Fatal("invalid client message was accepted")
			}
			protocolError := realtime.V2ProtocolErrorFrom(err)
			if protocolError.Code != entry.ErrorCode || protocolError.Retryable {
				t.Fatalf("unexpected rejection: %+v", protocolError)
			}
		})
	}
}

func TestHouseRocketsClientContractBoundaries(t *testing.T) {
	valid := `{"protocolVersion":2,"messageId":"a","type":"houseRockets.steer","payload":{"controlGeneration":"g","inputSequence":1,"heading":7.853981633974483}}`
	decoded, err := realtime.DecodeV2ClientMessage([]byte(valid))
	if err != nil || math.Abs(decoded.Steer.Heading-math.Pi/2) > 1e-12 {
		t.Fatalf("heading normalization failed: %v", err)
	}
	for _, command := range []string{"gameSession.join", "gameSession.leave", "gameSession.cancel", "houseRockets.resync"} {
		if _, err := realtime.DecodeV2ClientMessage([]byte(`{"protocolVersion":2,"messageId":"a","type":"` + command + `"}`)); err != nil {
			t.Fatal(err)
		}
	}
	invalid := []string{
		strings.Replace(valid, `"messageId":"a"`, `"messageId":"`+strings.Repeat("a", 129)+`"`, 1),
		strings.Replace(valid, `"controlGeneration":"g"`, `"controlGeneration":"`+strings.Repeat("g", 129)+`"`, 1),
		strings.Replace(valid, `"controlGeneration":"g"`, `"controlGeneration":" g"`, 1),
		strings.Replace(valid, `"inputSequence":1`, `"inputSequence":9223372036854775808`, 1),
		strings.Replace(valid, `"inputSequence":1`, `"inputSequence":-1`, 1),
		strings.Replace(valid, `"heading":7.853981633974483`, `"heading":"NaN"`, 1),
		strings.Replace(valid, `"heading":7.853981633974483`, `"Heading":0`, 1),
		strings.Replace(valid, `"heading":7.853981633974483`, `"heading":0,"Heading":1`, 1),
		strings.Replace(valid, `"protocolVersion":2`, `"protocolVersion":null`, 1),
		strings.Replace(valid, `"messageId":"a"`, `"messageId":null`, 1),
		strings.Repeat(" ", 16*1024+1), `[]`, `null`, `true`, `{`,
	}
	for index, message := range invalid {
		if _, err := realtime.DecodeV2ClientMessage([]byte(message)); err == nil {
			t.Fatalf("invalid boundary %d accepted", index)
		}
	}
	for _, heading := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if (houseRockets.HouseRocketsSteerModel{ControlGeneration: "g", InputSequence: 1, Heading: heading}).Valid() {
			t.Fatal("nonfinite heading accepted")
		}
	}
}

func TestHouseRocketsServerContractFixtures(t *testing.T) {
	fixture := readHouseRocketsFixture[houseRocketsProtocolFixture](t, "houseRocketsProtocol")
	for _, entry := range fixture.ServerMessages {
		t.Run(entry.Name, func(t *testing.T) {
			switch entry.Name {
			case "welcome":
				roundTripHouseRocketsMessage[houseRockets.HouseRocketsWelcomeModel](t, entry.Message)
			case "pong":
				roundTripHouseRocketsMessage[dtos.RealtimePongModel](t, entry.Message)
			case "accepted":
				roundTripHouseRocketsMessage[struct{}](t, entry.Message)
			case "sessionSnapshot":
				roundTripHouseRocketsMessage[dtos.GameSessionResponseModel](t, entry.Message)
			case "controlGranted":
				roundTripHouseRocketsMessage[houseRockets.HouseRocketsControlGrantedModel](t, entry.Message)
			case "countdown", "playing", "recovering":
				roundTripHouseRocketsMessage[houseRockets.HouseRocketsSnapshotModel](t, entry.Message)
				var message realtime.V2ServerMessage[houseRockets.HouseRocketsSnapshotModel]
				if err := json.Unmarshal(entry.Message, &message); err != nil {
					t.Fatal(err)
				}
				if message.Sequence == nil || *message.Sequence != message.Payload.StateSequence {
					t.Fatal("envelope and game sequence differ")
				}
				if math.Abs(message.Payload.ElapsedSeconds-float64(message.Payload.Tick)/120) > 1e-12 {
					t.Fatal("tick/elapsed units differ")
				}
			case "result", "drawResult", "cancelledResult", "cancelledBeforeStartResult":
				roundTripHouseRocketsMessage[houseRockets.HouseRocketsResultModel](t, entry.Message)
				var message realtime.V2ServerMessage[houseRockets.HouseRocketsResultModel]
				if err := json.Unmarshal(entry.Message, &message); err != nil {
					t.Fatal(err)
				}
				assertHouseRocketsResultPolicy(t, message.Payload)
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
	for _, data := range []json.RawMessage{fixture.HTTP.Ensure.Response, fixture.HTTP.Discover.Response} {
		var response core.ApiResponse[dtos.GameSessionResponseModel]
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		assertHouseRocketsJSON(t, data, response)
		if response.Data.Rules.ReadyWindowMilliseconds != 30000 || response.Data.Rules.CountdownMilliseconds != 3000 {
			t.Fatal("duration encoded in wrong units")
		}
		model := response.Data
		snapshot := gameDomain.SessionSnapshot{
			SessionID: model.SessionId, HouseID: model.HouseId, GameKey: houseRockets.GameKey,
			ProtocolVersion: houseRockets.ProtocolVersion, Mode: gameDomain.RealtimeGame,
			State: gameDomain.SessionCountdown, Rules: houseRockets.Definition().Rules,
			CreatedBy: model.CreatedBy, CreatedAt: model.CreatedAt.Time, UpdatedAt: model.UpdatedAt.Time,
			CountdownEndsAt: model.CountdownEndsAt.Time, Version: model.Version,
		}
		for _, player := range model.Players {
			snapshot.Players = append(snapshot.Players, gameDomain.SessionPlayer{
				PlayerID: player.PlayerId, State: gameDomain.PlayerReady, JoinedAt: player.JoinedAt.Time, ReadyAt: player.ReadyAt.Time,
			})
		}
		assertHouseRocketsJSON(t, data, core.Success(dtos.GameSessionToResponseModel(snapshot)))
	}
	var result core.ApiResponse[houseRockets.HouseRocketsResultModel]
	if err := json.Unmarshal(fixture.HTTP.Result.Response, &result); err != nil {
		t.Fatal(err)
	}
	assertHouseRocketsJSON(t, fixture.HTTP.Result.Response, result)
	if fixture.HTTP.Ensure.Request.HouseId != result.Data.HouseID {
		t.Fatal("HTTP house identities differ")
	}

	localTime := time.Date(2026, 10, 3, 15, 0, 0, 125000000, time.FixedZone("local", 3*3600))
	encoded, err := json.Marshal(realtime.NewV2ServerMessage(realtime.PongMessageType, "", nil, localTime, struct{}{}))
	if err != nil || !strings.Contains(string(encoded), `"sentAt":"2026-10-03T12:00:00.125Z"`) || strings.Contains(string(encoded), "sequence") {
		t.Fatal("UTC or optional sequence shape changed")
	}
	rejection, err := json.Marshal(realtime.NewV2ErrorMessage("a", localTime, realtime.V2ProtocolError{Code: houseRockets.StaleControlErrorCode}))
	if err != nil || !strings.Contains(string(rejection), `"args":[]`) || strings.Contains(string(rejection), "payload") {
		t.Fatal("error arguments must be an array, not omitted/null")
	}
}

func assertHouseRocketsResultPolicy(t *testing.T, result houseRockets.HouseRocketsResultModel) {
	t.Helper()
	if result.Status == houseRockets.ResultCancelled {
		if result.WinnerID != nil {
			t.Fatal("cancelled match must not have a winner")
		}
		if result.EndReason == houseRockets.EndSessionExpired && (result.DurationSeconds != 300 || result.StartedAt == nil) {
			t.Fatal("expired match must have played for five minutes")
		}
		if result.StartedAt == nil && result.DurationSeconds != 0 {
			t.Fatal("match cancelled before start has no playing duration")
		}
		for _, player := range result.Players {
			if player.Rank != nil {
				t.Fatal("cancelled results do not award ranks")
			}
		}
		return
	}
	winners := 0
	for _, player := range result.Players {
		rank := 1
		if player.EliminatedAtTick == nil {
			winners++
			if result.WinnerID == nil || *result.WinnerID != player.PlayerID {
				t.Fatal("survivor must be the winner")
			}
		} else {
			for _, other := range result.Players {
				if other.EliminatedAtTick == nil || *other.EliminatedAtTick > *player.EliminatedAtTick {
					rank++
				}
			}
		}
		if player.Rank == nil || *player.Rank != rank {
			t.Fatal("competition ranks must tie on the same elimination tick, not distance")
		}
	}
	if result.EndReason == houseRockets.EndSimultaneousElimination && (result.WinnerID != nil || winners != 0) {
		t.Fatal("simultaneous last elimination is a draw")
	}
	if result.EndReason == houseRockets.EndLastSurvivor && winners != 1 {
		t.Fatal("exactly one survivor required")
	}
}

func TestHouseRocketsSimulationReferenceBaseline(t *testing.T) {
	fixture := readHouseRocketsFixture[struct {
		SchemaVersion     int                `json:"schemaVersion"`
		ContractRevision  string             `json:"contractRevision"`
		CourseVersion     int                `json:"courseVersion"`
		AbsoluteTolerance float64            `json:"absoluteTolerance"`
		Constants         map[string]float64 `json:"constants"`
		Motions           []struct {
			Name     string `json:"name"`
			Expected struct {
				Tick           int     `json:"tick"`
				ElapsedSeconds float64 `json:"elapsedSeconds"`
			} `json:"expected"`
		} `json:"motions"`
	}](t, "houseRocketsSimulation")
	if fixture.SchemaVersion != 1 || fixture.ContractRevision != houseRockets.ContractRevision || fixture.CourseVersion != houseRockets.CourseVersion {
		t.Fatal("simulation reference revision differs")
	}
	expected := map[string]float64{
		"baseSpeed": houseRockets.BaseSpeed, "trackHeight": houseRockets.TrackHeight, "rocketRadius": houseRockets.RocketRadius,
		"spawnX": houseRockets.SpawnX, "leaderAnchorX": houseRockets.LeaderAnchorX, "viewportWidth": houseRockets.ViewportWidth,
		"physicsRateHz": houseRockets.PhysicsRateHz, "firstGateX": houseRockets.FirstGateX, "gateSpacing": houseRockets.GateSpacing,
		"transitionIntervalSeconds": houseRockets.TransitionIntervalSeconds, "transitionDurationSeconds": houseRockets.TransitionDurationSeconds,
		"speedFieldRadius": houseRockets.SpeedFieldRadius, "boostMultiplier": houseRockets.BoostMultiplier,
		"boostDurationSeconds": houseRockets.BoostDurationSeconds, "slowMultiplier": houseRockets.SlowMultiplier, "slowDurationSeconds": houseRockets.SlowDurationSeconds,
	}
	if !reflect.DeepEqual(fixture.Constants, expected) || fixture.AbsoluteTolerance != 1e-9 || len(fixture.Motions) != 6 {
		t.Fatal("Swift reference constants or case coverage changed")
	}
	for _, motion := range fixture.Motions {
		if math.Abs(float64(motion.Expected.Tick)/120-motion.Expected.ElapsedSeconds) > fixture.AbsoluteTolerance {
			t.Fatal("reference tick is not a 1/120 second step")
		}
	}
}

func FuzzHouseRocketsClientDecoder(f *testing.F) {
	f.Add([]byte(`{"protocolVersion":2,"messageId":"a","type":"gameSession.join"}`))
	f.Add([]byte(`{"protocolVersion":2,"messageId":"a","type":"gameSession.setReady","payload":{"ready":false}}`))
	f.Add([]byte(`{"protocolVersion":2,"messageId":"a","type":"houseRockets.steer","payload":{"controlGeneration":"g","inputSequence":1,"heading":0}}`))
	f.Add([]byte(`{"protocolVersion":2,"messageId":"a","type":"gameSession.join","payload":{"x":[{"x":1,"x":2}]}}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		decoded, err := realtime.DecodeV2ClientMessage(data)
		if err != nil {
			return
		}
		if decoded.Envelope.ProtocolVersion != 2 || decoded.Envelope.MessageID == "" || len(data) > 16*1024 {
			t.Fatal("invalid envelope accepted")
		}
		if decoded.Steer != nil && (!decoded.Steer.Valid() || math.Abs(decoded.Steer.Heading) > math.Pi) {
			t.Fatal("invalid or unnormalized steer accepted")
		}
	})
}
