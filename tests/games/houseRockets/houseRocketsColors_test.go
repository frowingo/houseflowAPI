package houseRockets

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games"
	"houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/data/migrations"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit/gamekit"
)

func rocketsFixtureMessage[T any](t *testing.T, fixture houseRocketsProtocolFixture, name string) realtime.V2ServerMessage[T] {
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

// renderRockets renders a frame payload for a spectator, as the gateway does.
func renderRockets(t *testing.T, payload json.RawMessage) (json.RawMessage, houseRockets.HouseRocketsSnapshotModel) {
	t.Helper()
	view, err := houseRockets.Module().PrepareFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := view.Render(module.Viewer{})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot houseRockets.HouseRocketsSnapshotModel
	if err := json.Unmarshal(rendered, &snapshot); err != nil {
		t.Fatal(err)
	}
	return rendered, snapshot
}

func rocketColors(snapshot houseRockets.HouseRocketsSnapshotModel) []houseRockets.Color {
	colors := make([]houseRockets.Color, len(snapshot.Players))
	for seat, player := range snapshot.Players {
		colors[seat] = player.Color
	}
	return colors
}

func rocketsPlayers(ids ...string) []module.Player {
	players := make([]module.Player, len(ids))
	for seat, id := range ids {
		players[seat] = module.Player{PlayerID: id, DisplayName: "Player " + id}
	}
	return players
}

func rocketsLobbyState(claims map[string]houseRockets.Color) json.RawMessage {
	if claims == nil {
		return nil
	}
	state, _ := json.Marshal(map[string]any{"colors": claims})
	return state
}

func previewRockets(t *testing.T, players []module.Player, lobbyState json.RawMessage) json.RawMessage {
	t.Helper()
	frame, err := houseRockets.Module().PreviewFrame(module.PreviewParams{SessionID: "colors-session", HouseID: "colors-house", CountdownEndsAt: time.Now().UTC(),
		Players: players, Epoch: 7, Sequence: 4, LobbyState: lobbyState})
	if err != nil {
		t.Fatal(err)
	}
	return frame.Payload
}

// startRockets starts a runtime through the module, as the room owner does,
// and returns its world as a frame.
func startRockets(t *testing.T, players []module.Player, lobbyState json.RawMessage) json.RawMessage {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	runtime, err := houseRockets.Module().NewRuntime(module.RuntimeParams{SessionID: "colors-session", HouseID: "colors-house", StartedAt: now, Players: players,
		Epoch: 7, Now: now, InitialSequence: 4, LobbyState: lobbyState})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := runtime.RecoveryFrame()
	if err != nil {
		t.Fatal(err)
	}
	return frame.Payload
}

// The fixture's claimed sessionSnapshot carries the lobby state the server
// itself writes (ADR-030): replaying each joined player's claim through the
// module yields the fixture's lobbyState exactly.
func TestHouseRocketsSessionSnapshotLobbyState(t *testing.T) {
	fixture := readHouseRocketsFixture[houseRocketsProtocolFixture](t, "houseRocketsProtocol")
	message := rocketsFixtureMessage[dtos.GameSessionResponseModel](t, fixture, "claimedColorsSessionSnapshot")
	var claims struct {
		Colors map[string]houseRockets.Color `json:"colors"`
	}
	if err := json.Unmarshal(message.Payload.LobbyState, &claims); err != nil || len(claims.Colors) == 0 || message.Payload.GameKey != houseRockets.GameKey {
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
		intent, _ := json.Marshal(houseRockets.HouseRocketsSelectColorModel{Color: color})
		state, err := houseRockets.ApplyLobbyIntent(session, player.PlayerID, houseRockets.SelectColorMessageType, intent)
		if err != nil {
			t.Fatal(err)
		}
		session.LobbyState = state
	}
	gamekit.AssertHouseRocketsJSON(t, message.Payload.LobbyState, json.RawMessage(session.LobbyState))
	// The plain sessionSnapshot has no claims, and no lobbyState field.
	plain := rocketsFixtureMessage[dtos.GameSessionResponseModel](t, fixture, "sessionSnapshot")
	if plain.Payload.LobbyState != nil {
		t.Fatalf("plain sessionSnapshot lobbyState = %s", plain.Payload.LobbyState)
	}
}

// The countdown fixtures show the module's colors: the plain one is the seat
// order, the claimed one is the claimed sessionSnapshot's lobby state
// assigned to the same roster (claims first, then the first free color).
func TestHouseRocketsColorFixturesFollowTheLobby(t *testing.T) {
	fixture := readHouseRocketsFixture[houseRocketsProtocolFixture](t, "houseRocketsProtocol")
	session := rocketsFixtureMessage[dtos.GameSessionResponseModel](t, fixture, "claimedColorsSessionSnapshot")
	for name, lobbyState := range map[string]json.RawMessage{"countdown": nil, "claimedColorsCountdown": session.Payload.LobbyState} {
		countdown := rocketsFixtureMessage[houseRockets.HouseRocketsSnapshotModel](t, fixture, name).Payload
		players := make([]module.Player, len(countdown.Players))
		for seat, player := range countdown.Players {
			players[seat] = module.Player{PlayerID: player.PlayerID, DisplayName: player.DisplayName}
		}
		if countdown.SessionID != session.Payload.SessionId || len(players) != len(session.Payload.Players) {
			t.Fatalf("%s follows the sessionSnapshot roster", name)
		}
		_, preview := renderRockets(t, previewRockets(t, players, lobbyState))
		if !slices.Equal(rocketColors(preview), rocketColors(countdown)) {
			t.Fatalf("%s colors = %v, preview %v", name, rocketColors(countdown), rocketColors(preview))
		}
	}
	// The registered module takes the fixture's selectColor as a lobby intent.
	registry, err := games.NewRegistry(games.Options{Realtime: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Lobby(houseRockets.GameKey); !ok {
		t.Fatal("houseRockets has no lobby intents")
	}
}

// Preview and runtime assign the same colors from the same roster and lobby
// state: by seat, claims kept, the others the first free catalog color.
func TestHouseRocketsPreviewAndRuntimeAgreeOnColors(t *testing.T) {
	reversed := houseRockets.PlayerColors()
	slices.Reverse(reversed)
	eight := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"}
	everyone := map[string]houseRockets.Color{}
	for seat, id := range eight {
		everyone[id] = reversed[seat]
	}
	for name, test := range map[string]struct {
		ids    []string
		claims map[string]houseRockets.Color
		want   []houseRockets.Color
	}{
		"some claims": {[]string{"a", "b", "c"}, map[string]houseRockets.Color{"b": houseRockets.ColorMint, "c": houseRockets.ColorTeal},
			[]houseRockets.Color{houseRockets.ColorCoral, houseRockets.ColorMint, houseRockets.ColorTeal}},
		"same color twice, earlier seat wins": {[]string{"a", "b", "c"}, map[string]houseRockets.Color{"c": houseRockets.ColorGold, "b": houseRockets.ColorGold},
			[]houseRockets.Color{houseRockets.ColorMint, houseRockets.ColorGold, houseRockets.ColorCoral}},
		"no claims":    {eight, nil, houseRockets.PlayerColors()},
		"eight claims": {eight, everyone, reversed},
	} {
		players := rocketsPlayers(test.ids...)
		lobbyState := rocketsLobbyState(test.claims)
		_, preview := renderRockets(t, previewRockets(t, players, lobbyState))
		_, running := renderRockets(t, startRockets(t, players, lobbyState))
		if !slices.Equal(rocketColors(preview), test.want) || !slices.Equal(rocketColors(running), test.want) {
			t.Fatalf("%s: preview %v, runtime %v, want %v", name, rocketColors(preview), rocketColors(running), test.want)
		}
	}
}

// Without claims the frames are byte for byte what an empty lobby state
// gives: the colors are the release before lobby colors' seat order.
func TestHouseRocketsUnclaimedColorsKeepTheSeatOrder(t *testing.T) {
	players := rocketsPlayers("a", "b", "c")
	for _, empty := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{"colors":{}}`), json.RawMessage(`{"colors":{"gone":"mint"}}`)} {
		plain, snapshot := renderRockets(t, previewRockets(t, players, nil))
		other, _ := renderRockets(t, previewRockets(t, players, empty))
		// The preview's countdown deadline is now; drop it before comparing.
		var a, b map[string]any
		_ = json.Unmarshal(plain, &a)
		_ = json.Unmarshal(other, &b)
		delete(a, "countdownEndsAt")
		delete(b, "countdownEndsAt")
		if !bytes.Equal(gamekit.MustJSON(t, a), gamekit.MustJSON(t, b)) || !slices.Equal(rocketColors(snapshot), houseRockets.PlayerColors()[:3]) {
			t.Fatalf("%s:\n%s\n%s", empty, plain, other)
		}
	}
}

// Colors are part of the simulation state and survive recovery. A checkpoint
// of the release before lobby colors (seat colors) still restores; a roster
// with a color that is unknown, missing or held twice does not.
func TestHouseRocketsCheckpointKeepsColors(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	players := []houseRockets.PlayerIdentity{{PlayerID: "a", DisplayName: "A"}, {PlayerID: "b", DisplayName: "B"}, {PlayerID: "c", DisplayName: "C"}}
	start := func(lobbyState json.RawMessage) houseRockets.RuntimeCheckpoint {
		runtime, err := houseRockets.NewRuntime(houseRockets.NewSimulationParams{SessionID: "colors-session", HouseID: "colors-house", StartedAt: now,
			Players: players, LobbyState: lobbyState}, 7, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.Advance(now.Add(100 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		return runtime.Checkpoint()
	}
	colorsOf := func(state houseRockets.SimulationState) []houseRockets.Color {
		colors := make([]houseRockets.Color, len(state.Players))
		for seat, player := range state.Players {
			colors[seat] = player.Color
		}
		return colors
	}
	restore := func(checkpoint houseRockets.RuntimeCheckpoint) (*houseRockets.Runtime, error) {
		// Through JSON, as the checkpoint is stored.
		encoded, err := json.Marshal(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		var stored houseRockets.RuntimeCheckpoint
		if err := json.Unmarshal(encoded, &stored); err != nil {
			t.Fatal(err)
		}
		return houseRockets.RestoreRuntime(stored, 8, checkpoint.CapturedAt.Add(time.Second))
	}

	want := []houseRockets.Color{houseRockets.ColorPink, houseRockets.ColorMint, houseRockets.ColorCoral}
	claimed := start(rocketsLobbyState(map[string]houseRockets.Color{"a": houseRockets.ColorPink}))
	if !slices.Equal(colorsOf(claimed.World), want) {
		t.Fatalf("started colors: %v", colorsOf(claimed.World))
	}
	restored, err := restore(claimed)
	if err != nil || !slices.Equal(colorsOf(restored.Checkpoint().World), want) {
		t.Fatalf("restored colors: %v", err)
	}
	frame, _ := json.Marshal(restored.RecoveryFrame())
	if _, snapshot := renderRockets(t, frame); !slices.Equal(rocketColors(snapshot), want) {
		t.Fatalf("recovery frame colors: %v", rocketColors(snapshot))
	}

	legacy := start(nil)
	if restored, err := restore(legacy); err != nil || !slices.Equal(colorsOf(restored.Checkpoint().World), houseRockets.PlayerColors()[:3]) {
		t.Fatalf("a checkpoint with the seat colors restores: %v", err)
	}

	recolor := func(color houseRockets.Color) houseRockets.RuntimeCheckpoint {
		edited := claimed
		edited.World.Players = slices.Clone(claimed.World.Players)
		edited.World.Players[2].Color = color
		return edited
	}
	// Any distinct catalog colors restore, not only what assignColors gives.
	if restored, err := restore(recolor(houseRockets.ColorViolet)); err != nil || restored.Checkpoint().World.Players[2].Color != houseRockets.ColorViolet {
		t.Fatalf("a distinct catalog color restores: %v", err)
	}
	for name, color := range map[string]houseRockets.Color{"duplicate": houseRockets.ColorPink, "unknown": "plum", "missing": ""} {
		if _, err := restore(recolor(color)); err == nil {
			t.Fatalf("%s color accepted", name)
		}
	}
}

// The lobby intent through the session command, with the real module: the
// first claim wins, a retry is idempotent, a reused command ID with another
// color is refused, a leaver's color is freed and the countdown closes the
// lobby.
func TestHouseRocketsLobbyIntentCommands(t *testing.T) {
	fixture := gamekit.NewTurnGameFixture(t)
	definition := houseRockets.Definition()
	definition.Rules.MaximumPlayers = 2 // two ready players start the countdown
	catalog, err := gamekit.NewDefinitionCatalog(definition)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := module.NewRegistry(houseRockets.Module())
	if err != nil {
		t.Fatal(err)
	}
	policy := fixture.MembershipPolicy()
	ensure := gameCommands.NewEnsureActiveGameSessionHandler(fixture.Repository, catalog, policy)
	join, ready, leave := fixture.Join, fixture.Ready, fixture.Leave
	lobby := gameCommands.NewApplyLobbyIntentHandler(fixture.Repository, policy, registry)
	claim := func(sessionID, commandID, playerID, color string) (gameDomain.SessionSnapshot, error) {
		return lobby.Handle(fixture.Ctx, gameCommands.ApplyLobbyIntentCommand{CommandID: commandID, SessionID: sessionID, UserID: playerID,
			IntentType: houseRockets.SelectColorMessageType, Payload: json.RawMessage(`{"color":"` + color + `"}`)})
	}
	colors := func(snapshot gameDomain.SessionSnapshot) map[string]houseRockets.Color {
		var lobby struct {
			Colors map[string]houseRockets.Color `json:"colors"`
		}
		_ = json.Unmarshal(snapshot.LobbyState, &lobby)
		return lobby.Colors
	}
	owner, member := fixture.OwnerID, fixture.MemberID
	session, err := ensure.Handle(fixture.Ctx, gameCommands.EnsureActiveGameSessionCommand{CommandID: "ensure", UserID: owner, HouseID: fixture.HouseID, GameKey: houseRockets.GameKey})
	if err != nil {
		t.Fatal(err)
	}
	id := session.SessionID
	for _, player := range []string{owner, member} {
		if session, err = join.Handle(fixture.Ctx, gameCommands.JoinGameSessionCommand{CommandID: "join-" + player, SessionID: id, UserID: player}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := claim(id, "owner-teal", owner, "teal")
	if err != nil || first.Version != session.Version+1 || colors(first)[owner] != houseRockets.ColorTeal {
		t.Fatalf("first claim: %v %s", err, first.LobbyState)
	}
	if retried, err := claim(id, "owner-teal", owner, "teal"); err != nil || retried.Version != first.Version || string(retried.LobbyState) != string(first.LobbyState) {
		t.Fatalf("retry: %v", err)
	}
	if _, err := claim(id, "owner-teal", owner, "mint"); !helpers.IsApplicationError(err, "game.error.command_id_reused") {
		t.Fatalf("reused command: %v", err)
	}
	if _, err := claim(id, "owner-red", owner, "red"); !helpers.IsApplicationError(err, houseRockets.InvalidInputErrorCode) {
		t.Fatalf("unknown color: %v", err)
	}
	taken, err := claim(id, "member-teal", member, "teal")
	if err != nil || taken.Version != first.Version || string(taken.LobbyState) != string(first.LobbyState) {
		t.Fatalf("someone else's color is accepted and changes nothing: %v %s", err, taken.LobbyState)
	}
	switched, err := claim(id, "owner-mint", owner, "mint")
	if err != nil || len(colors(switched)) != 1 || colors(switched)[owner] != houseRockets.ColorMint {
		t.Fatalf("switch: %v %s", err, switched.LobbyState)
	}
	freed, err := claim(id, "member-teal-again", member, "teal")
	if err != nil || colors(freed)[member] != houseRockets.ColorTeal || colors(freed)[owner] != houseRockets.ColorMint {
		t.Fatalf("the switched-from color is free: %v %s", err, freed.LobbyState)
	}
	// A leaver's claim is dropped with the next intent.
	if _, err := leave.Handle(fixture.Ctx, gameCommands.LeaveGameSessionCommand{CommandID: "member-leaves", SessionID: id, UserID: member}); err != nil {
		t.Fatal(err)
	}
	if _, err := claim(id, "member-gone", member, "pink"); !helpers.IsApplicationError(err, houseRockets.NotInLobbyErrorCode) {
		t.Fatalf("leaver: %v", err)
	}
	if afterLeave, err := claim(id, "owner-takes-teal", owner, "teal"); err != nil || len(colors(afterLeave)) != 1 || colors(afterLeave)[owner] != houseRockets.ColorTeal {
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
	if _, err := claim(id, "late", member, "pink"); !helpers.IsApplicationError(err, houseRockets.NotInLobbyErrorCode) {
		t.Fatalf("countdown: %v", err)
	}
	if colors(session)[owner] != houseRockets.ColorTeal || len(colors(session)) != 1 {
		t.Fatalf("the claims stay: %s", session.LobbyState)
	}
}

// Migration 0063 localizes the lobby errors the session command returns,
// and running it again changes nothing.
func TestHouseRocketsLobbyLocalizationMigration(t *testing.T) {
	var lobby migrations.Migration
	for _, migration := range migrations.AllMigrations() {
		if migration.Version() == "0063" {
			lobby = migration
		}
	}
	if lobby == nil || lobby.Name() != "houseRocketsLobbyLocalizations" {
		t.Fatal("migration 0063 is not registered")
	}
	if module.ErrorCode(houseRockets.GameKey, "not_in_lobby") != houseRockets.NotInLobbyErrorCode ||
		module.ErrorCode(houseRockets.GameKey, "invalid_input") != houseRockets.InvalidInputErrorCode {
		t.Fatal("error codes")
	}
	fixture := gamekit.NewGameSessionPersistenceFixture(t)
	for range 2 {
		if err := lobby.Up(fixture.Ctx, fixture.DB); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range []string{houseRockets.NotInLobbyErrorCode, houseRockets.InvalidInputErrorCode} {
		count, err := fixture.DB.Collection("localization").CountDocuments(fixture.Ctx, map[string]any{"key": code})
		if err != nil || count != 2 {
			t.Fatalf("%s: %d documents, %v", code, count, err)
		}
	}
}
