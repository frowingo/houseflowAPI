package tests

import (
	"encoding/json"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	fausSwitch "houseflowApi/internal/application/game/games/fausSwitch"
	ticTacToe "houseflowApi/internal/application/game/games/ticTacToe"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/dtos"
)

// lobbyTicTacToe is Tic-Tac-Toe with Faus Switch's color claims as lobby
// intents: the core's lobby path is game-agnostic, so any lane can carry it.
type lobbyTicTacToe struct{ module.TurnModule }

func (lobbyTicTacToe) DecodeLobbyIntent(messageType string, payload json.RawMessage) (json.RawMessage, error) {
	return fausSwitch.DecodeLobbyIntent(messageType, payload)
}

func (lobbyTicTacToe) ApplyLobbyIntent(session gameDomain.SessionSnapshot, playerID, messageType string, intent json.RawMessage) (json.RawMessage, error) {
	return fausSwitch.ApplyLobbyIntent(session, playerID, messageType, intent)
}

func lobbyGames(t *testing.T) *module.Registry {
	t.Helper()
	registry, err := module.NewRegistry(lobbyTicTacToe{ticTacToe.Module()})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func colorsOf(t *testing.T, state json.RawMessage) map[string]fausSwitch.Color {
	t.Helper()
	var lobby struct {
		Colors map[string]fausSwitch.Color `json:"colors"`
	}
	if len(state) > 0 {
		if err := json.Unmarshal(state, &lobby); err != nil {
			t.Fatal(err)
		}
	}
	return lobby.Colors
}

func TestGameSessionLobbyStateRules(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	session := newGameSession(t, now, gameDomain.SessionRules{MinimumPlayers: 2, MaximumPlayers: 3, ReadyWindowDuration: time.Minute, CountdownDuration: time.Minute})
	session.ClearPendingEvents()
	state := json.RawMessage(`{"colors":{"a":"plum"}}`)
	if err := session.UpdateLobbyState("a", state, now); err != gameDomain.ErrPlayerNotFound {
		t.Fatalf("not joined: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		if err := session.JoinPlayer(id, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []string{``, `[]`, `"plum"`, `{"colors":`, `null`} {
		if err := session.UpdateLobbyState("a", json.RawMessage(bad), now); err != gameDomain.ErrLobbyStateRequired {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	version := session.Version()
	if err := session.UpdateLobbyState("a", state, now); err != nil || session.Version() != version+1 {
		t.Fatalf("update: %v, version %d", err, session.Version())
	}
	assertEventTypePresent(t, session.PendingEvents(), gameDomain.LobbyStateChanged)
	if err := session.UpdateLobbyState("a", state, now); err != nil || session.Version() != version+1 {
		t.Fatal("the same state adds no version")
	}
	restored, err := gameDomain.RestoreGameSession(session.Snapshot())
	if err != nil || string(restored.Snapshot().LobbyState) != string(state) {
		t.Fatalf("restore: %v %s", err, restored.Snapshot().LobbyState)
	}
	// The snapshot is a copy.
	snapshot := session.Snapshot()
	snapshot.LobbyState[2] = 'X'
	if string(session.Snapshot().LobbyState) != string(state) {
		t.Fatal("snapshot shares the lobby state")
	}
	invalid := session.Snapshot()
	invalid.LobbyState = json.RawMessage(`[1]`)
	if _, err := gameDomain.RestoreGameSession(invalid); err == nil {
		t.Fatal("a lobby state is a JSON object")
	}
	// Ready window still takes choices; the countdown fixes them.
	for _, id := range []string{"a", "b"} {
		if err := session.SetReady(id, true, now); err != nil {
			t.Fatal(err)
		}
	}
	if session.State() != gameDomain.SessionReadyWindow || session.UpdateLobbyState("b", json.RawMessage(`{"colors":{"a":"plum","b":"ocean"}}`), now) != nil {
		t.Fatalf("ready window: %s", session.State())
	}
	if err := session.Advance(now.Add(time.Minute)); err != nil || session.State() != gameDomain.SessionCountdown {
		t.Fatalf("countdown: %v %s", err, session.State())
	}
	if err := session.UpdateLobbyState("a", state, now.Add(time.Minute)); err != gameDomain.ErrLobbyClosed {
		t.Fatalf("countdown: %v", err)
	}
}

// Only a game that keeps a lobby state writes the field: every other game's
// sessionSnapshot is byte for byte what it was.
func TestSessionSnapshotWritesLobbyStateOnlyWhenSet(t *testing.T) {
	now := time.Now().UTC()
	session := newGameSession(t, now, gameDomain.SessionRules{MinimumPlayers: 2, MaximumPlayers: 2})
	encoded, _ := json.Marshal(dtos.GameSessionToResponseModel(session.Snapshot()))
	var fields map[string]json.RawMessage
	if json.Unmarshal(encoded, &fields) != nil || fields["lobbyState"] != nil {
		t.Fatalf("empty lobby state is written: %s", encoded)
	}
	snapshot := session.Snapshot()
	snapshot.LobbyState = json.RawMessage(`{"colors":{"a":"plum"}}`)
	encoded, _ = json.Marshal(dtos.GameSessionToResponseModel(snapshot))
	if json.Unmarshal(encoded, &fields) != nil || string(fields["lobbyState"]) != `{"colors":{"a":"plum"}}` {
		t.Fatalf("lobby state: %s", encoded)
	}
}

func TestLobbyIntentDecoding(t *testing.T) {
	game, _ := lobbyGames(t).Game(ticTacToe.GameKey)
	envelope := func(kind, payload string) []byte {
		return []byte(`{"protocolVersion":2,"messageId":"m-1","type":"` + kind + `","payload":` + payload + `}`)
	}
	decoded, err := realtime.DecodeV2ClientMessage(envelope(fausSwitch.SelectColorMessageType, `{"color":"plum"}`), game)
	if err != nil || string(decoded.LobbyIntent) != `{"color":"plum"}` || decoded.Intent != nil || decoded.Move != nil {
		t.Fatalf("lobby intent: %+v %v", decoded, err)
	}
	for _, payload := range []string{`{"color":"red"}`, `{"color":"plum","x":1}`, `{}`} {
		_, err := realtime.DecodeV2ClientMessage(envelope(fausSwitch.SelectColorMessageType, payload), game)
		if code := realtime.V2ProtocolErrorFrom(ticTacToe.GameKey, err).Code; code != module.ErrorCode(ticTacToe.GameKey, "invalid_input") {
			t.Fatalf("%s: %s", payload, code)
		}
	}
	// A move still decodes as a move.
	if decoded, err := realtime.DecodeV2ClientMessage(envelope(ticTacToe.MoveMessageType, `{"cell":4}`), game); err != nil || decoded.Move == nil || decoded.LobbyIntent != nil {
		t.Fatalf("move: %+v %v", decoded, err)
	}
	// A game without lobby intents does not know the type.
	plain, _ := rocketsGames().Game(ticTacToe.GameKey)
	if _, err := realtime.DecodeV2ClientMessage(envelope(fausSwitch.SelectColorMessageType, `{"color":"plum"}`), plain); err == nil {
		t.Fatal("a game without lobby intents accepted one")
	}
}

type lobbyFixture struct {
	*turnGameFixture
	lobby *gameCommands.ApplyLobbyIntentHandler
}

func newLobbyFixture(t *testing.T) *lobbyFixture {
	t.Helper()
	fixture := &lobbyFixture{turnGameFixture: newTurnGameFixture(t)}
	fixture.lobby = gameCommands.NewApplyLobbyIntentHandler(fixture.repository, fixture.membershipPolicy(), lobbyGames(t))
	return fixture
}

func (fixture *lobbyFixture) claim(sessionID, commandID, playerID, color string) (gameDomain.SessionSnapshot, error) {
	return fixture.lobby.Handle(fixture.ctx, gameCommands.ApplyLobbyIntentCommand{CommandID: commandID, SessionID: sessionID, UserID: playerID,
		IntentType: fausSwitch.SelectColorMessageType, Payload: json.RawMessage(`{"color":"` + color + `"}`)})
}

func (fixture *lobbyFixture) joinAll(t *testing.T, playerIDs ...string) gameDomain.SessionSnapshot {
	t.Helper()
	snapshot, err := fixture.ensure.Handle(fixture.ctx, gameCommands.EnsureActiveGameSessionCommand{CommandID: fixture.commandID("ensure"), UserID: fixture.ownerID, HouseID: fixture.houseID, GameKey: ticTacToe.GameKey})
	if err != nil {
		t.Fatal(err)
	}
	for _, playerID := range playerIDs {
		if snapshot, err = fixture.join.Handle(fixture.ctx, gameCommands.JoinGameSessionCommand{CommandID: fixture.commandID("join"), SessionID: snapshot.SessionID, UserID: playerID}); err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

// Briefing §5: the first claim of a color wins; a claim of someone else's
// color is accepted and changes nothing; a player may switch; a leaver's
// color is free again; every change commits with its receipt.
func TestLobbyIntentsCommitColorClaims(t *testing.T) {
	fixture := newLobbyFixture(t)
	owner, member := fixture.ownerID, fixture.memberID
	session := fixture.joinAll(t, owner)
	id := session.SessionID
	if _, err := fixture.claim(id, "early", member, "plum"); !helpers.IsApplicationError(err, "ticTacToe.error.not_in_lobby") {
		t.Fatalf("not joined: %v", err)
	}
	first, err := fixture.claim(id, "owner-plum", owner, "plum")
	if err != nil || first.Version != session.Version+1 || colorsOf(t, first.LobbyState)[owner] != fausSwitch.ColorPlum {
		t.Fatalf("first claim: %v %s", err, first.LobbyState)
	}
	if retried, err := fixture.claim(id, "owner-plum", owner, "plum"); err != nil || retried.Version != first.Version {
		t.Fatalf("retry: %v", err)
	}
	if _, err := fixture.claim(id, "owner-plum", owner, "ocean"); !helpers.IsApplicationError(err, "game.error.command_id_reused") {
		t.Fatalf("reused command: %v", err)
	}
	if _, err := fixture.claim(id, "bad", owner, "red"); !helpers.IsApplicationError(err, "ticTacToe.error.invalid_input") {
		t.Fatalf("unknown color: %v", err)
	}
	if _, err := fixture.claim(id, "outsider", fixture.outsiderID, "ocean"); err == nil {
		t.Fatal("an outsider claimed a color")
	}
	joined := fixture.joinAll(t, member)
	taken, err := fixture.claim(id, "member-plum", member, "plum")
	if err != nil || taken.Version != joined.Version || string(taken.LobbyState) != string(first.LobbyState) {
		t.Fatalf("someone else's color: %v %s", err, taken.LobbyState)
	}
	if _, err := fixture.claim(id, "member-ocean", member, "ocean"); err != nil {
		t.Fatal(err)
	}
	switched, err := fixture.claim(id, "owner-forest", owner, "forest")
	if colors := colorsOf(t, switched.LobbyState); err != nil || colors[owner] != fausSwitch.ColorForest || colors[member] != fausSwitch.ColorOcean || len(colors) != 2 {
		t.Fatalf("switch: %v %s", err, switched.LobbyState)
	}
	if _, err := fixture.leave.Handle(fixture.ctx, gameCommands.LeaveGameSessionCommand{CommandID: "member-leaves", SessionID: id, UserID: member}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.claim(id, "member-gone", member, "plum"); !helpers.IsApplicationError(err, "ticTacToe.error.not_in_lobby") {
		t.Fatalf("leaver: %v", err)
	}
	freed, err := fixture.claim(id, "owner-ocean", owner, "ocean")
	if colors := colorsOf(t, freed.LobbyState); err != nil || colors[owner] != fausSwitch.ColorOcean || len(colors) != 1 {
		t.Fatalf("the leaver's color is free: %v %s", err, freed.LobbyState)
	}
	// MongoDB round trip keeps the state, canonically.
	stored, err := fixture.repository.FindByID(fixture.ctx, id)
	if err != nil || string(stored.Snapshot().LobbyState) != string(freed.LobbyState) {
		t.Fatalf("stored: %v %s", err, stored.Snapshot().LobbyState)
	}
}

func TestLobbyIntentsCloseAtTheCountdown(t *testing.T) {
	fixture := newLobbyFixture(t)
	session := fixture.joinAll(t, fixture.ownerID, fixture.memberID)
	if _, err := fixture.claim(session.SessionID, "plum", fixture.ownerID, "plum"); err != nil {
		t.Fatal(err)
	}
	var err error
	for _, playerID := range []string{fixture.ownerID, fixture.memberID} {
		if session, err = fixture.ready.Handle(fixture.ctx, gameCommands.SetPlayerReadyCommand{CommandID: fixture.commandID("ready"), SessionID: session.SessionID, UserID: playerID, Ready: true}); err != nil {
			t.Fatal(err)
		}
	}
	if session.State != gameDomain.SessionCountdown {
		t.Fatalf("state %s", session.State)
	}
	if _, err := fixture.claim(session.SessionID, "late", fixture.memberID, "ocean"); !helpers.IsApplicationError(err, "ticTacToe.error.not_in_lobby") {
		t.Fatalf("countdown: %v", err)
	}
	if colors := colorsOf(t, session.LobbyState); colors[fixture.ownerID] != fausSwitch.ColorPlum || len(colors) != 1 {
		t.Fatalf("the claims stay: %s", session.LobbyState)
	}
}

// End to end over two gateway instances: a lobby intent needs no control,
// is accepted, and every client sees the claims in its sessionSnapshot.
func TestLobbyIntentOverRealtimeGateway(t *testing.T) {
	definition := ticTacToe.Definition()
	fixture := newGameGatewayFixtureWith(t, definition, lobbyGames(t), 0, 0)
	now := time.Now().UTC()
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "lobby-colors-session", HouseID: fixture.houseID, GameKey: ticTacToe.GameKey,
		ProtocolVersion: ticTacToe.ProtocolVersion, Mode: gameDomain.TurnBasedGame, Rules: definition.Rules, CreatedBy: fixture.ownerID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fixture.ownerID, fixture.memberID} {
		if err := session.JoinPlayer(id, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.repository.Create(fixture.ctx, session.Snapshot(), session.PendingEvents(), persistenceCommand("lobby-colors-create", "create", "lobby")); err != nil {
		t.Fatal(err)
	}
	owner := fixture.connect(t, 0, session.Snapshot().SessionID, fixture.ownerID)
	member := fixture.connect(t, 1, session.Snapshot().SessionID, fixture.memberID)
	accepted := func(socket *rocketsSocket, messageID string) {
		t.Helper()
		socket.await(t, func(message realtime.ServerMessage) bool {
			if message.Type == realtime.CommandRejectedMessageType && message.MessageID == messageID {
				t.Fatalf("%s rejected: %+v", messageID, message.Error)
			}
			return message.Type == realtime.CommandAcceptedMessageType && message.MessageID == messageID
		})
	}
	seesColors := func(socket *rocketsSocket, want map[string]fausSwitch.Color) {
		t.Helper()
		socket.await(t, func(message realtime.ServerMessage) bool {
			if message.Type != realtime.GameSessionSnapshotEventType {
				return false
			}
			var snapshot dtos.GameSessionResponseModel
			if json.Unmarshal(message.Payload, &snapshot) != nil {
				return false
			}
			colors := colorsOf(t, snapshot.LobbyState)
			if len(colors) != len(want) {
				return false
			}
			for id, color := range want {
				if colors[id] != color {
					return false
				}
			}
			return true
		})
	}
	owner.send(t, fausSwitch.SelectColorMessageType, "owner-plum", map[string]string{"color": "plum"})
	accepted(owner, "owner-plum")
	seesColors(member, map[string]fausSwitch.Color{fixture.ownerID: fausSwitch.ColorPlum})
	member.send(t, fausSwitch.SelectColorMessageType, "member-plum", map[string]string{"color": "plum"})
	accepted(member, "member-plum")
	member.send(t, fausSwitch.SelectColorMessageType, "member-olive", map[string]string{"color": "olive"})
	accepted(member, "member-olive")
	seesColors(owner, map[string]fausSwitch.Color{fixture.ownerID: fausSwitch.ColorPlum, fixture.memberID: fausSwitch.ColorOlive})
	member.send(t, fausSwitch.SelectColorMessageType, "member-red", map[string]string{"color": "red"})
	rejection := member.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "member-red"
	})
	if rejection.Error == nil || rejection.Error.Code != "ticTacToe.error.invalid_input" {
		t.Fatalf("unknown color: %+v", rejection.Error)
	}
}
