package houseTanks

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games"
	"houseflowApi/internal/application/game/games/houseTanks"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/data/migrations"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit/gamekit"
)

func tanksFixtureMessage[T any](t *testing.T, fixture houseTanksProtocolFixture, name string) realtime.V2ServerMessage[T] {
	t.Helper()
	for _, entry := range fixture.ServerMessages {
		if entry.Name != name {
			continue
		}
		var message realtime.V2ServerMessage[T]
		if err := json.Unmarshal(entry.Message, &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	t.Fatalf("no %s fixture", name)
	return realtime.V2ServerMessage[T]{}
}

// renderTanks renders a frame payload for a spectator, as the gateway does.
func renderTanks(t *testing.T, payload json.RawMessage) (json.RawMessage, houseTanks.HouseTanksSnapshotModel) {
	t.Helper()
	view, err := houseTanks.Module().PrepareFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := view.Render(module.Viewer{})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot houseTanks.HouseTanksSnapshotModel
	if err := json.Unmarshal(rendered, &snapshot); err != nil {
		t.Fatal(err)
	}
	return rendered, snapshot
}

func tankColors(snapshot houseTanks.HouseTanksSnapshotModel) []houseTanks.Color {
	colors := make([]houseTanks.Color, len(snapshot.Players))
	for seat, player := range snapshot.Players {
		colors[seat] = player.Color
	}
	return colors
}

func tanksPlayers(ids ...string) []module.Player {
	players := make([]module.Player, len(ids))
	for seat, id := range ids {
		players[seat] = module.Player{PlayerID: id, DisplayName: "Player " + id}
	}
	return players
}

func tanksLobbyState(claims map[string]houseTanks.Color) json.RawMessage {
	state, _ := json.Marshal(map[string]any{"colors": claims})
	return state
}

// startTanks starts a runtime through the module, as the room owner does,
// and returns it with its first frame.
func startTanks(t *testing.T, players []module.Player, lobbyState json.RawMessage) (module.TickRuntime, json.RawMessage) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	runtime, err := houseTanks.Module().NewRuntime(module.RuntimeParams{SessionID: "colors-session", HouseID: "colors-house", StartedAt: now, Players: players,
		Epoch: 7, Now: now, InitialSequence: 4, LobbyState: lobbyState})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	update, err := runtime.NextUpdate(ctx)
	if err != nil || update.Frame == nil {
		t.Fatalf("first frame: %+v %v", update, err)
	}
	return runtime, update.Frame.Payload
}

func previewTanks(t *testing.T, players []module.Player, lobbyState json.RawMessage) json.RawMessage {
	t.Helper()
	frame, err := houseTanks.Module().PreviewFrame(module.PreviewParams{SessionID: "colors-session", HouseID: "colors-house", CountdownEndsAt: time.Now().UTC(),
		Players: players, Epoch: 7, Sequence: 4, LobbyState: lobbyState})
	if err != nil {
		t.Fatal(err)
	}
	return frame.Payload
}

// The fixture's sessionSnapshot carries the lobby state the server itself
// writes (ADR-030): replaying each joined player's claim through the module
// yields the fixture's lobbyState exactly.
func TestHouseTanksSessionSnapshotLobbyState(t *testing.T) {
	fixture := readHouseTanksFixture[houseTanksProtocolFixture](t, "houseTanksProtocol")
	message := tanksFixtureMessage[dtos.GameSessionResponseModel](t, fixture, "sessionSnapshot")
	var claims struct {
		Colors map[string]houseTanks.Color `json:"colors"`
	}
	if err := json.Unmarshal(message.Payload.LobbyState, &claims); err != nil || len(claims.Colors) == 0 || message.Payload.GameKey != houseTanks.GameKey {
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
		intent, _ := json.Marshal(houseTanks.HouseTanksSelectColorModel{Color: color})
		state, err := houseTanks.ApplyLobbyIntent(session, player.PlayerID, houseTanks.SelectColorMessageType, intent)
		if err != nil {
			t.Fatal(err)
		}
		session.LobbyState = state
	}
	gamekit.AssertHouseRocketsJSON(t, message.Payload.LobbyState, json.RawMessage(session.LobbyState))
}

// The countdown fixtures show the module's colors: the plain one is the seat
// order, the claimed one is the sessionSnapshot's lobby state assigned to the
// same roster (claims first, then the first free catalog color).
func TestHouseTanksColorFixturesFollowTheLobby(t *testing.T) {
	fixture := readHouseTanksFixture[houseTanksProtocolFixture](t, "houseTanksProtocol")
	session := tanksFixtureMessage[dtos.GameSessionResponseModel](t, fixture, "sessionSnapshot")
	for name, lobbyState := range map[string]json.RawMessage{"countdown": nil, "claimedColorsCountdown": session.Payload.LobbyState} {
		countdown := tanksFixtureMessage[houseTanks.HouseTanksSnapshotModel](t, fixture, name).Payload
		players := make([]module.Player, len(countdown.Players))
		for seat, player := range countdown.Players {
			players[seat] = module.Player{PlayerID: player.PlayerID, DisplayName: player.DisplayName}
		}
		if name == "claimedColorsCountdown" && (countdown.SessionID != session.Payload.SessionId || len(players) != len(session.Payload.Players)) {
			t.Fatal("the claimed countdown follows the sessionSnapshot")
		}
		_, preview := renderTanks(t, previewTanks(t, players, lobbyState))
		if !slices.Equal(tankColors(preview), tankColors(countdown)) {
			t.Fatalf("%s colors = %v, preview %v", name, tankColors(countdown), tankColors(preview))
		}
	}
	// The registered module takes the fixture's selectColor as a lobby intent.
	registry, err := games.NewRegistry(games.Options{Realtime: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Lobby(houseTanks.GameKey); !ok {
		t.Fatal("houseTanks has no lobby intents")
	}
}

// Preview and runtime assign the same colors from the same roster and lobby
// state, up to eight players with eight different claims.
func TestHouseTanksPreviewAndRuntimeAgreeOnColors(t *testing.T) {
	reversed := houseTanks.PlayerColors()
	slices.Reverse(reversed)
	eight := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"}
	everyone := map[string]houseTanks.Color{}
	for seat, id := range eight {
		everyone[id] = reversed[seat]
	}
	for name, test := range map[string]struct {
		ids    []string
		claims map[string]houseTanks.Color
		want   []houseTanks.Color
	}{
		"some claims":  {[]string{"a", "b", "c"}, map[string]houseTanks.Color{"b": houseTanks.ColorPlum, "c": houseTanks.ColorSky}, []houseTanks.Color{houseTanks.ColorOrange, houseTanks.ColorPlum, houseTanks.ColorSky}},
		"no claims":    {eight, nil, houseTanks.PlayerColors()},
		"eight claims": {eight, everyone, reversed},
	} {
		players := tanksPlayers(test.ids...)
		lobbyState := tanksLobbyState(test.claims)
		_, preview := renderTanks(t, previewTanks(t, players, lobbyState))
		_, first := startTanks(t, players, lobbyState)
		_, running := renderTanks(t, first)
		if !slices.Equal(tankColors(preview), test.want) || !slices.Equal(tankColors(running), test.want) {
			t.Fatalf("%s: preview %v, runtime %v, want %v", name, tankColors(preview), tankColors(running), test.want)
		}
	}
}

// stripFrameColors turns a frame into one of the release before lobby
// colors, whose frame players had no color.
func stripFrameColors(t *testing.T, payload json.RawMessage) json.RawMessage {
	t.Helper()
	var frame map[string]any
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatal(err)
	}
	for _, player := range frame["world"].(map[string]any)["players"].([]any) {
		delete(player.(map[string]any), "color")
	}
	stripped, _ := json.Marshal(frame)
	return stripped
}

// Without claims a snapshot is byte for byte the one the release before
// lobby colors rendered, and a frame of that release (no color) still
// renders: with the seat colors, which is what a claim lost to a rolling
// deploy falls back to.
func TestHouseTanksUnclaimedColorsKeepTheWireBytes(t *testing.T) {
	players := tanksPlayers("a", "b", "c")
	for name, payload := range map[string]json.RawMessage{"preview": previewTanks(t, players, nil), "runtime": func() json.RawMessage {
		_, first := startTanks(t, players, nil)
		return first
	}()} {
		rendered, snapshot := renderTanks(t, payload)
		legacy, _ := renderTanks(t, stripFrameColors(t, payload))
		if !bytes.Equal(rendered, legacy) || !slices.Equal(tankColors(snapshot), houseTanks.PlayerColors()[:3]) {
			t.Fatalf("%s:\n%s\n%s", name, rendered, legacy)
		}
	}
	claimed := previewTanks(t, players, tanksLobbyState(map[string]houseTanks.Color{"a": houseTanks.ColorIvory}))
	if _, snapshot := renderTanks(t, stripFrameColors(t, claimed)); !slices.Equal(tankColors(snapshot), houseTanks.PlayerColors()[:3]) {
		t.Fatalf("an older frame renders the seat colors: %v", tankColors(snapshot))
	}
}

// Colors are part of the checkpoint and survive recovery; a checkpoint of the
// release before lobby colors restores with the seat colors.
func TestHouseTanksCheckpointKeepsColors(t *testing.T) {
	players := tanksPlayers("a", "b", "c")
	want := []houseTanks.Color{houseTanks.ColorIvory, houseTanks.ColorPlum, houseTanks.ColorOrange}
	runtime, _ := startTanks(t, players, tanksLobbyState(map[string]houseTanks.Color{"a": houseTanks.ColorIvory}))
	checkpoint := runtime.Checkpoint()
	encoded, err := json.Marshal(checkpoint.State)
	if err != nil {
		t.Fatal(err)
	}
	restore := func(payload json.RawMessage) (*houseTanks.Runtime, error) {
		var stored houseTanks.RuntimeCheckpoint
		if err := json.Unmarshal(payload, &stored); err != nil {
			t.Fatal(err)
		}
		return houseTanks.RestoreRuntime(stored, 8, checkpoint.CapturedAt.Add(time.Second))
	}
	restoredColors := func(restored *houseTanks.Runtime) []houseTanks.Color {
		frame, _ := json.Marshal(restored.RecoveryFrame())
		_, snapshot := renderTanks(t, frame)
		return tankColors(snapshot)
	}
	restored, err := restore(encoded)
	if err != nil || !slices.Equal(restoredColors(restored), want) {
		t.Fatalf("restored colors: %v", err)
	}
	for seat, player := range restored.Checkpoint().Session.Players {
		if player.Color != want[seat] {
			t.Fatal("the next checkpoint keeps the colors")
		}
	}
	edit := func(change func(players []any)) json.RawMessage {
		// UseNumber keeps the match's 64-bit random state exact.
		var document map[string]any
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&document); err != nil {
			t.Fatal(err)
		}
		change(document["session"].(map[string]any)["players"].([]any))
		edited, _ := json.Marshal(document)
		return edited
	}
	legacy := edit(func(players []any) {
		for _, player := range players {
			delete(player.(map[string]any), "color")
		}
	})
	if restored, err := restore(legacy); err != nil || !slices.Equal(restoredColors(restored), houseTanks.PlayerColors()[:3]) {
		t.Fatalf("an older checkpoint keeps the seat colors: %v", err)
	}
	for name, change := range map[string]func(players []any){
		"mixed":     func(players []any) { delete(players[1].(map[string]any), "color") },
		"duplicate": func(players []any) { players[1].(map[string]any)["color"] = "ivory" },
		"unknown":   func(players []any) { players[1].(map[string]any)["color"] = "forest" },
	} {
		if _, err := restore(edit(change)); err == nil {
			t.Fatalf("%s colors accepted", name)
		}
	}
}

// The lobby intent through the session command, with the real module: the
// first claim wins, a retry is idempotent, a reused command ID with another
// color is refused, and the countdown closes the lobby.
func TestHouseTanksLobbyIntentCommands(t *testing.T) {
	fixture := gamekit.NewTurnGameFixture(t)
	definition := houseTanks.Definition()
	definition.Rules.MaximumPlayers = 2 // two ready players start the countdown
	catalog, err := gamekit.NewDefinitionCatalog(definition)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := module.NewRegistry(houseTanks.Module())
	if err != nil {
		t.Fatal(err)
	}
	policy := fixture.MembershipPolicy()
	ensure := gameCommands.NewEnsureActiveGameSessionHandler(fixture.Repository, catalog, policy)
	join, ready, leave := fixture.Join, fixture.Ready, fixture.Leave
	lobby := gameCommands.NewApplyLobbyIntentHandler(fixture.Repository, policy, registry)
	claim := func(sessionID, commandID, playerID, color string) (gameDomain.SessionSnapshot, error) {
		return lobby.Handle(fixture.Ctx, gameCommands.ApplyLobbyIntentCommand{CommandID: commandID, SessionID: sessionID, UserID: playerID,
			IntentType: houseTanks.SelectColorMessageType, Payload: json.RawMessage(`{"color":"` + color + `"}`)})
	}
	colors := func(snapshot gameDomain.SessionSnapshot) map[string]houseTanks.Color {
		var lobby struct {
			Colors map[string]houseTanks.Color `json:"colors"`
		}
		_ = json.Unmarshal(snapshot.LobbyState, &lobby)
		return lobby.Colors
	}
	owner, member := fixture.OwnerID, fixture.MemberID
	session, err := ensure.Handle(fixture.Ctx, gameCommands.EnsureActiveGameSessionCommand{CommandID: "ensure", UserID: owner, HouseID: fixture.HouseID, GameKey: houseTanks.GameKey})
	if err != nil {
		t.Fatal(err)
	}
	id := session.SessionID
	for _, player := range []string{owner, member} {
		if session, err = join.Handle(fixture.Ctx, gameCommands.JoinGameSessionCommand{CommandID: "join-" + player, SessionID: id, UserID: player}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := claim(id, "owner-sky", owner, "sky")
	if err != nil || first.Version != session.Version+1 || colors(first)[owner] != houseTanks.ColorSky {
		t.Fatalf("first claim: %v %s", err, first.LobbyState)
	}
	if retried, err := claim(id, "owner-sky", owner, "sky"); err != nil || retried.Version != first.Version || string(retried.LobbyState) != string(first.LobbyState) {
		t.Fatalf("retry: %v", err)
	}
	if _, err := claim(id, "owner-sky", owner, "plum"); !helpers.IsApplicationError(err, "game.error.command_id_reused") {
		t.Fatalf("reused command: %v", err)
	}
	if _, err := claim(id, "owner-red", owner, "red"); !helpers.IsApplicationError(err, houseTanks.InvalidInputErrorCode) {
		t.Fatalf("unknown color: %v", err)
	}
	taken, err := claim(id, "member-sky", member, "sky")
	if err != nil || taken.Version != first.Version || string(taken.LobbyState) != string(first.LobbyState) {
		t.Fatalf("someone else's color is accepted and changes nothing: %v %s", err, taken.LobbyState)
	}
	switched, err := claim(id, "owner-plum", owner, "plum")
	if err != nil || len(colors(switched)) != 1 || colors(switched)[owner] != houseTanks.ColorPlum {
		t.Fatalf("switch: %v %s", err, switched.LobbyState)
	}
	freed, err := claim(id, "member-sky-again", member, "sky")
	if err != nil || colors(freed)[member] != houseTanks.ColorSky || colors(freed)[owner] != houseTanks.ColorPlum {
		t.Fatalf("the switched-from color is free: %v %s", err, freed.LobbyState)
	}
	// A leaver's claim is dropped with the next intent.
	if _, err := leave.Handle(fixture.Ctx, gameCommands.LeaveGameSessionCommand{CommandID: "member-leaves", SessionID: id, UserID: member}); err != nil {
		t.Fatal(err)
	}
	if _, err := claim(id, "member-gone", member, "ivory"); !helpers.IsApplicationError(err, houseTanks.NotInLobbyErrorCode) {
		t.Fatalf("leaver: %v", err)
	}
	if afterLeave, err := claim(id, "owner-takes-sky", owner, "sky"); err != nil || len(colors(afterLeave)) != 1 || colors(afterLeave)[owner] != houseTanks.ColorSky {
		t.Fatalf("the leaver's color is free: %v %s", err, afterLeave.LobbyState)
	}
	// The countdown fixes the claims.
	if session, err = join.Handle(fixture.Ctx, gameCommands.JoinGameSessionCommand{CommandID: "member-rejoins", SessionID: id, UserID: member}); err != nil {
		t.Fatal(err)
	}
	for _, player := range []string{owner, member} {
		if session, err = ready.Handle(fixture.Ctx, gameCommands.SetPlayerReadyCommand{CommandID: "ready-" + player, SessionID: id, UserID: player, Ready: true}); err != nil {
			t.Fatal(err)
		}
	}
	if session.State != gameDomain.SessionCountdown {
		t.Fatalf("state %s", session.State)
	}
	if _, err := claim(id, "late", member, "ivory"); !helpers.IsApplicationError(err, houseTanks.NotInLobbyErrorCode) {
		t.Fatalf("countdown: %v", err)
	}
	if colors(session)[owner] != houseTanks.ColorSky || len(colors(session)) != 1 {
		t.Fatalf("the claims stay: %s", session.LobbyState)
	}
}

// Migration 0062 localizes the lobby errors the session command returns,
// and running it again changes nothing.
func TestHouseTanksLobbyLocalizationMigration(t *testing.T) {
	var lobby migrations.Migration
	for _, migration := range migrations.AllMigrations() {
		if migration.Version() == "0062" {
			lobby = migration
		}
	}
	if lobby == nil || lobby.Name() != "houseTanksLobbyLocalizations" {
		t.Fatal("migration 0062 is not registered")
	}
	if module.ErrorCode(houseTanks.GameKey, "not_in_lobby") != houseTanks.NotInLobbyErrorCode ||
		module.ErrorCode(houseTanks.GameKey, "invalid_input") != houseTanks.InvalidInputErrorCode {
		t.Fatal("error codes")
	}
	fixture := gamekit.NewGameSessionPersistenceFixture(t)
	for range 2 {
		if err := lobby.Up(fixture.Ctx, fixture.DB); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range []string{houseTanks.NotInLobbyErrorCode, houseTanks.InvalidInputErrorCode} {
		count, err := fixture.DB.Collection("localization").CountDocuments(fixture.Ctx, map[string]any{"key": code})
		if err != nil || count != 2 {
			t.Fatalf("%s: %d documents, %v", code, count, err)
		}
	}
}
