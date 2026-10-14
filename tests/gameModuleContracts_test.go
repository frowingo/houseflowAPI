package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/infrastructure/realtime"
)

const concreteGamePackagePrefix = "houseflowApi/internal/application/game/games/"

// Only the composition root and a game's own package name a concrete game; the
// core, transport, persistence and controllers depend on the module contract.
func TestOnlyGameCompositionImportsConcreteGames(t *testing.T) {
	root := filepath.Join(applicationDirectory(t), "..", "..")
	composition := filepath.Join(root, "internal", "application", "game", "games")
	for _, directory := range []string{filepath.Join(root, "internal"), filepath.Join(root, "cmd")} {
		err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return walkErr
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imported := range parsed.Imports {
				importPath, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					return err
				}
				if !strings.HasPrefix(importPath, concreteGamePackagePrefix) {
					continue
				}
				gameDirectory := filepath.Join(composition, strings.Split(strings.TrimPrefix(importPath, concreteGamePackagePrefix), "/")[0])
				if filepath.Dir(path) != composition && !strings.HasPrefix(path, gameDirectory+string(filepath.Separator)) {
					relative, _ := filepath.Rel(root, path)
					t.Errorf("%s imports concrete game %s", relative, importPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Every tick module must satisfy what the shared core relies on. The checks run
// against each enabled module, so a new game inherits them.
func TestTickModulesHonourTheCoreContract(t *testing.T) {
	verified := 0
	for _, registered := range rocketsGames().Games() {
		if game, tick := registered.(module.TickModule); tick {
			verified++
			t.Run(game.Definition().GameKey, func(t *testing.T) { verifyTickModule(t, game) })
		}
	}
	if verified == 0 {
		t.Fatal("no tick module to verify")
	}
}

// Every turn module must satisfy what the turn engine relies on.
func TestTurnModulesHonourTheCoreContract(t *testing.T) {
	verified := 0
	for _, registered := range rocketsGames().Games() {
		if game, turn := registered.(module.TurnModule); turn {
			verified++
			t.Run(game.Definition().GameKey, func(t *testing.T) { verifyTurnModule(t, game) })
		}
	}
	if verified == 0 {
		t.Fatal("no turn module to verify")
	}
}

func verifyTurnModule(t *testing.T, game module.TurnModule) {
	definition := game.Definition()
	if definition.ProtocolVersion != realtime.V2ProtocolVersion || definition.Mode != gameDomain.TurnBasedGame {
		t.Fatalf("definition = %+v", definition)
	}
	defaults, err := game.ResolveConfig(module.Config{Rules: definition.Rules})
	if err != nil || defaults.Rules != definition.Rules || !json.Valid(defaults.Settings) {
		t.Fatalf("default config = %+v %v", defaults, err)
	}
	if _, err := game.ResolveConfig(module.Config{Rules: definition.Rules, Settings: json.RawMessage(`{"unknownSetting":1}`)}); !errors.Is(err, module.ErrInvalidConfig) {
		t.Fatalf("unknown setting accepted: %v", err)
	}
	for name, value := range map[string]any{"welcome": game.Welcome("contract-session", "contract-connection", nil), "clientSettings": game.ClientSettings(defaults.Settings)} {
		if encoded, err := json.Marshal(value); err != nil || len(encoded) < 2 || encoded[0] != '{' {
			t.Fatalf("%s = %s %v", name, encoded, err)
		}
	}
	if _, err := game.DecodeMove(definition.GameKey+".unknownMove", json.RawMessage(`{}`)); !errors.Is(err, module.ErrUnsupportedIntent) {
		t.Fatalf("unknown move: %v", err)
	}

	session := runningContractSession(t, definition)
	session.Settings = defaults.Settings
	now := session.StartedAt
	started, err := game.Start(module.Turn{Session: session, Now: now})
	if err != nil || started.Result != nil || len(started.State) == 0 || started.State[0] != '{' || !json.Valid(started.State) || (!started.Deadline.IsZero() && !started.Deadline.After(now)) {
		t.Fatalf("start = %+v %v", started, err)
	}
	session.GameState, session.TurnDeadline = started.State, started.Deadline
	// An unchanged roster must not change the state: the engine reconciles
	// after every command and would otherwise write a version each time.
	unchanged, err := game.Reconcile(module.Turn{Session: session, Now: now})
	if err != nil || unchanged.Result != nil || !sameJSONObject(t, unchanged.State, session.GameState) || !unchanged.Deadline.Equal(session.TurnDeadline) {
		t.Fatalf("reconcile without changes = %+v %v", unchanged, err)
	}
	for _, viewer := range []string{"contract-a", ""} {
		view, err := game.View(module.Turn{Session: session, Now: now}, viewer)
		if encoded, marshalErr := json.Marshal(view); err != nil || marshalErr != nil || len(encoded) < 2 || encoded[0] != '{' {
			t.Fatalf("view for %q = %s %v", viewer, encoded, err)
		}
	}

	// A cancelled result reconstructed from the session must finalize against
	// the terminal session and decode as the game's wire model.
	cancelledAt := now.Add(time.Second)
	cancelled, err := game.CancellationResult(session, module.EndCancelledByUser, cancelledAt)
	if err != nil || cancelled.Validate() != nil || cancelled.Status != gameDomain.MatchCancelled {
		t.Fatalf("cancellation result = %+v %v", cancelled, err)
	}
	terminal, err := gameDomain.RestoreGameSession(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := terminal.Cancel(cancelled.EndReason, cancelled.EndedAt); err != nil {
		t.Fatal(err)
	}
	finalized, err := game.FinalizeResult(cancelled, terminal.Snapshot())
	if err != nil || finalized.Validate() != nil {
		t.Fatalf("finalize cancelled = %+v %v", finalized, err)
	}
	var identity struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(finalized.Payload, &identity); err != nil || identity.SessionID != session.SessionID {
		t.Fatalf("result payload must carry its session ID: %s", finalized.Payload)
	}
	if _, err := game.DecodeResult(finalized); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}

func sameJSONObject(t *testing.T, first, second json.RawMessage) bool {
	t.Helper()
	var left, right any
	return json.Unmarshal(first, &left) == nil && json.Unmarshal(second, &right) == nil && reflect.DeepEqual(left, right)
}

func verifyTickModule(t *testing.T, game module.TickModule) {
	definition := game.Definition()
	timing := game.Timing(nil)
	if definition.ProtocolVersion != realtime.V2ProtocolVersion || definition.Mode != gameDomain.RealtimeGame {
		t.Fatalf("definition = %+v", definition)
	}
	if timing.ControlTimeout <= 0 || timing.InputLifetime <= 0 || timing.MaximumIntentRate <= 0 {
		t.Fatalf("timing = %+v", timing)
	}
	// The defaults must resolve; a session without stored settings (written by
	// the previous release) behaves exactly like one created with them.
	defaults, err := game.ResolveConfig(module.Config{Rules: definition.Rules})
	if err != nil || defaults.Rules != definition.Rules || len(defaults.Settings) == 0 {
		t.Fatalf("default config = %+v %v", defaults, err)
	}
	if resolved := game.Timing(defaults.Settings); resolved != timing {
		t.Fatalf("default settings timing = %+v, want %+v", resolved, timing)
	}
	if _, err := game.ResolveConfig(module.Config{Rules: definition.Rules, Settings: json.RawMessage(`{"unknownSetting":1}`)}); !errors.Is(err, module.ErrInvalidConfig) {
		t.Fatalf("unknown setting accepted: %v", err)
	}
	tooFew := definition.Rules
	tooFew.MinimumPlayers = 1
	if _, err := game.ResolveConfig(module.Config{Rules: tooFew}); err == nil {
		t.Fatal("rules below the core minimum accepted")
	}
	if settings, err := json.Marshal(game.ClientSettings(defaults.Settings)); err != nil || len(settings) < 2 || settings[0] != '{' {
		t.Fatalf("client settings = %s %v", settings, err)
	}
	if _, err := game.DecodeIntent(definition.GameKey+".unknownIntent", json.RawMessage(`{}`)); !errors.Is(err, module.ErrUnsupportedIntent) {
		t.Fatalf("unknown intent: %v", err)
	}
	if welcome, err := json.Marshal(game.Welcome("contract-session", "contract-connection", nil)); err != nil || len(welcome) < 2 || welcome[0] != '{' {
		t.Fatalf("welcome = %s %v", welcome, err)
	}
	session := runningContractSession(t, definition)
	players := []module.Player{{PlayerID: "contract-a", DisplayName: "A"}, {PlayerID: "contract-b", DisplayName: "B"}}

	preview, err := game.PreviewFrame(module.PreviewParams{SessionID: session.SessionID, HouseID: session.HouseID, CountdownEndsAt: session.StartedAt, Players: players, Epoch: 1, Sequence: 3})
	if err != nil {
		t.Fatal(err)
	}
	if header := decodeFrameHeader(t, preview.Payload); header.Phase != module.PhaseCountdown || header.RuntimeEpoch != 1 || header.StateSequence != 3 || preview.Sequence != 3 || header.World.SessionID != session.SessionID {
		t.Fatalf("preview header = %+v", header)
	}
	if previewView, err := game.PrepareFrame(preview.Payload); err != nil || !reflect.DeepEqual(previewView.Header(), decodeFrameHeader(t, preview.Payload)) {
		t.Fatalf("preview view header: %v", err)
	}

	runtime, err := game.NewRuntime(module.RuntimeParams{SessionID: session.SessionID, HouseID: session.HouseID, StartedAt: session.StartedAt, Players: players, Epoch: 1, Now: session.StartedAt})
	if err != nil {
		t.Fatal(err)
	}
	update, err := runtime.NextUpdate(context.Background())
	if err != nil || update.Frame == nil || update.Checkpoint == nil {
		t.Fatalf("first update = %+v %v", update, err)
	}
	header := decodeFrameHeader(t, update.Frame.Payload)
	if header.Phase != module.PhasePlaying || header.RuntimeEpoch != 1 || header.StateSequence != update.Frame.Sequence || len(header.World.Players) != len(players) || !header.Alive("contract-a") {
		t.Fatalf("frame header = %+v", header)
	}
	// Connections without a private view share one encoding, so their
	// snapshots must not depend on who is watching.
	view, err := game.PrepareFrame(update.Frame.Payload)
	if err != nil {
		t.Fatal(err)
	}
	// The transport routes frames by the view's header; it must describe the
	// published payload exactly.
	if !reflect.DeepEqual(view.Header(), header) {
		t.Fatalf("view header = %+v, payload header = %+v", view.Header(), header)
	}
	spectators := []module.Viewer{{PlayerID: "contract-a", ConnectionID: "contract-connection"}, {PlayerID: "contract-outsider", ConnectionID: "other-connection"}}
	first, err := view.Render(spectators[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := view.Render(spectators[1])
	if err != nil || view.ViewKey(spectators[0]) != view.ViewKey(spectators[1]) || !bytes.Equal(first, second) || !json.Valid(first) {
		t.Fatalf("spectator views differ: %s %s %v", first, second, err)
	}

	checkpoint := runtime.Checkpoint()
	state, err := json.Marshal(checkpoint.State)
	if err != nil || checkpoint.Epoch != 1 || checkpoint.Sequence <= 0 || checkpoint.CriticalKey == "" {
		t.Fatalf("checkpoint = %+v %v", checkpoint, err)
	}
	stored := module.Checkpoint{Epoch: checkpoint.Epoch, Sequence: checkpoint.Sequence, CapturedAt: checkpoint.CapturedAt}
	restoreAt := checkpoint.CapturedAt.Add(time.Millisecond)
	if _, err := game.RestoreRuntime(state, module.RestoreParams{Session: session, Epoch: 2, Now: restoreAt, Stored: stored}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	mismatched := stored
	mismatched.Sequence++
	if _, err := game.RestoreRuntime(state, module.RestoreParams{Session: session, Epoch: 2, Now: restoreAt, Stored: mismatched}); err == nil {
		t.Fatal("checkpoint restored under another header")
	}
	foreign := session
	foreign.SessionID = "another-session"
	if _, err := game.RestoreRuntime(state, module.RestoreParams{Session: foreign, Epoch: 2, Now: restoreAt, Stored: stored}); err == nil {
		t.Fatal("checkpoint restored into another session")
	}

	if err := runtime.Cancel(module.EndCancelledByUser); err != nil {
		t.Fatal(err)
	}
	proposed, ok, err := runtime.TakeResult()
	if err != nil || !ok {
		t.Fatalf("cancelled runtime proposed no result: %v", err)
	}
	finalized, err := game.FinalizeResult(proposed, session)
	if err != nil || finalized.GameKey != definition.GameKey || finalized.Status != gameDomain.MatchCancelled || finalized.EndReason != module.EndCancelledByUser {
		t.Fatalf("finalized = %+v %v", finalized, err)
	}
	if _, err := game.DecodeResult(finalized); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.CommittedFrame(finalized); err != nil {
		t.Fatal(err)
	}
	recovered, err := game.CancellationResult(session, module.EndRecoveryFailed, session.StartedAt.Add(time.Second))
	if err != nil || recovered.Status != gameDomain.MatchCancelled || recovered.EndReason != module.EndRecoveryFailed {
		t.Fatalf("recovery result = %+v %v", recovered, err)
	}
	if _, err := game.FinalizeResult(recovered, session); err != nil {
		t.Fatalf("recovery result rejected: %v", err)
	}
}

func runningContractSession(t *testing.T, definition gameDomain.GameDefinition) gameDomain.SessionSnapshot {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	rules := definition.Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 0, 0
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "contract-session", HouseID: "contract-house", GameKey: definition.GameKey, ProtocolVersion: definition.ProtocolVersion, Mode: definition.Mode, Rules: rules, CreatedBy: "contract-a", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"contract-a", "contract-b"} {
		if err := session.JoinPlayer(id, now); err != nil {
			t.Fatal(err)
		}
		if err := session.SetReady(id, true, now); err != nil {
			t.Fatal(err)
		}
	}
	if session.State() != gameDomain.SessionRunning {
		t.Fatalf("contract session state = %s", session.State())
	}
	return session.Snapshot()
}

func decodeFrameHeader(t *testing.T, payload json.RawMessage) module.FrameHeader {
	t.Helper()
	var header module.FrameHeader
	if err := json.Unmarshal(payload, &header); err != nil {
		t.Fatal(err)
	}
	return header
}

// During a rolling deploy the previous release (v37) exchanges gameplay input,
// frames, grants and checkpoints with this one through Redis. These are the
// field sets v37 writes and reads; changing one is a protocol change.
func TestCrossInstanceWireFormatsMatchPreviousRelease(t *testing.T) {
	game, _ := rocketsGames().Tick(houseRockets.GameKey)
	session := runningContractSession(t, game.Definition())
	players := []module.Player{{PlayerID: "contract-a", DisplayName: "A"}, {PlayerID: "contract-b", DisplayName: "B"}}
	runtime, err := game.NewRuntime(module.RuntimeParams{SessionID: session.SessionID, HouseID: session.HouseID, StartedAt: session.StartedAt, Players: players, Epoch: 1, Now: session.StartedAt})
	if err != nil {
		t.Fatal(err)
	}
	update, err := runtime.NextUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := game.PreviewFrame(module.PreviewParams{SessionID: session.SessionID, HouseID: session.HouseID, CountdownEndsAt: session.StartedAt, Players: players, Epoch: 1, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := game.DecodeIntent(houseRockets.SteerMessageType, json.RawMessage(`{"controlGeneration":"g","inputSequence":1,"heading":0.5}`))
	if err != nil {
		t.Fatal(err)
	}
	inputKeys := []string{"connectionId", "controlGeneration", "createdAt", "heading", "inputSequence", "kind", "messageId", "playerId", "runtimeEpoch"}
	for name, check := range map[string]struct {
		value any
		keys  []string
	}{
		"steerInput":   {intent.RuntimeInput(module.ControlInput{MessageID: "m", PlayerID: "p", ConnectionID: "c", RuntimeEpoch: 1, CreatedAt: time.Now()}), inputKeys},
		"controlInput": {module.ControlInput{MessageID: "m", Kind: module.InputBind, PlayerID: "p", ConnectionID: "c", RuntimeEpoch: 1, CreatedAt: time.Now()}, []string{"connectionId", "controlGeneration", "createdAt", "inputSequence", "kind", "messageId", "playerId", "runtimeEpoch"}},
		"grant":        {module.ControlGrant{SessionID: "s", PlayerID: "p", RuntimeEpoch: 1, ControlGeneration: "g"}, []string{"controlGeneration", "playerId", "runtimeEpoch", "sessionId"}},
		"frame":        {json.RawMessage(update.Frame.Payload), []string{"controls", "phase", "runtimeEpoch", "stateSequence", "world"}},
		"previewFrame": {json.RawMessage(preview.Payload), []string{"controls", "countdownEndsAt", "phase", "runtimeEpoch", "stateSequence", "world"}},
		"checkpoint":   {runtime.Checkpoint().State, []string{"capturedAt", "controls", "epoch", "proposedResult", "remainder", "schemaVersion", "sequence", "world"}},
	} {
		if keys := jsonObjectKeys(t, check.value); strings.Join(keys, ",") != strings.Join(check.keys, ",") {
			t.Errorf("%s fields = %v, want %v", name, keys, check.keys)
		}
	}
	// A core control input must decode as the previous release's runtime input.
	encoded, _ := json.Marshal(module.ControlInput{Kind: module.InputBind, PlayerID: "p", ConnectionID: "c", RuntimeEpoch: 4})
	var previous houseRockets.RuntimeInput
	if err := json.Unmarshal(encoded, &previous); err != nil || previous.Kind != houseRockets.InputBind || previous.RuntimeEpoch != 4 {
		t.Fatalf("control input as runtime input = %+v %v", previous, err)
	}
}

func jsonObjectKeys(t *testing.T, value any) []string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
