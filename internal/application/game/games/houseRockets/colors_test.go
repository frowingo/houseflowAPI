package houseRockets

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
)

func lobbyOf(state string, players map[string]gameDomain.PlayerState) gameDomain.SessionSnapshot {
	session := gameDomain.SessionSnapshot{LobbyState: json.RawMessage(state)}
	for _, id := range []string{"a", "b", "c", "d"} {
		if playerState, ok := players[id]; ok {
			session.Players = append(session.Players, gameDomain.SessionPlayer{PlayerID: id, State: playerState})
		}
	}
	return session
}

func TestColorClaims(t *testing.T) {
	for _, step := range []struct {
		name, state, player string
		color               Color
		want                string
	}{
		{"first claim", ``, "a", ColorCoral, `{"colors":{"a":"coral"}}`},
		{"someone else's color", `{"colors":{"a":"coral"}}`, "b", ColorCoral, `{"colors":{"a":"coral"}}`},
		{"own color again", `{"colors":{"a":"coral"}}`, "a", ColorCoral, `{"colors":{"a":"coral"}}`},
		{"switch frees the old color", `{"colors":{"a":"coral"}}`, "a", ColorTeal, `{"colors":{"a":"teal"}}`},
		{"the freed color is claimable", `{"colors":{"a":"teal"}}`, "b", ColorCoral, `{"colors":{"a":"teal","b":"coral"}}`},
		{"a leaver's color is free", `{"colors":{"c":"coral","d":"pink"}}`, "a", ColorCoral, `{"colors":{"a":"coral"}}`},
		{"unreadable claims are dropped", `{"colors":{"b":"plum","":"coral"}}`, "a", ColorCoral, `{"colors":{"a":"coral"}}`},
		{"unreadable state is no claim", `not json`, "b", ColorOrange, `{"colors":{"b":"orange"}}`},
	} {
		players := map[string]gameDomain.PlayerState{"a": gameDomain.PlayerWaiting, "b": gameDomain.PlayerReady, "c": gameDomain.PlayerLeft}
		intent := json.RawMessage(`{"color":"` + string(step.color) + `"}`)
		got, err := ApplyLobbyIntent(lobbyOf(step.state, players), step.player, SelectColorMessageType, intent)
		if err != nil || string(got) != step.want {
			t.Fatalf("%s: %s %v, want %s", step.name, got, err, step.want)
		}
	}
}

func TestLobbyIntentDecoding(t *testing.T) {
	// The core probes the type with no payload.
	for _, other := range []string{SteerMessageType, ResyncMessageType, "houseTanks.selectColor"} {
		if _, err := DecodeLobbyIntent(other, nil); !errors.Is(err, module.ErrUnsupportedIntent) {
			t.Fatalf("%s: %v", other, err)
		}
		if _, err := ApplyLobbyIntent(gameDomain.SessionSnapshot{}, "a", other, nil); !errors.Is(err, module.ErrUnsupportedIntent) {
			t.Fatalf("apply %s: %v", other, err)
		}
	}
	if _, err := DecodeLobbyIntent(SelectColorMessageType, nil); err == nil || errors.Is(err, module.ErrUnsupportedIntent) {
		t.Fatalf("selectColor without payload: %v", err)
	}
	// "plum" is a FauseTanks color, not a rocket color.
	for _, payload := range []string{`{"color":"red"}`, `{"color":"plum"}`, `{"color":"Mint"}`, `{"color":"teal","x":1}`, `{"color":null}`, `{"color":"teal","color":"mint"}`, `{}`, `[]`, `"teal"`} {
		if _, err := DecodeLobbyIntent(SelectColorMessageType, json.RawMessage(payload)); !errors.Is(err, module.ErrInvalidInput) {
			t.Fatalf("%s: %v", payload, err)
		}
	}
	for _, color := range PlayerColors() {
		intent, err := DecodeLobbyIntent(SelectColorMessageType, json.RawMessage(`{ "color" : "`+string(color)+`" }`))
		if err != nil || string(intent) != `{"color":"`+string(color)+`"}` {
			t.Fatalf("canonical intent: %s %v", intent, err)
		}
	}
}

func TestAssignColors(t *testing.T) {
	eight := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"}
	// Without claims the colors are exactly the old seat order.
	for size := MinimumPlayers; size <= MaximumPlayers; size++ {
		if got := assignColors(eight[:size], nil); !slices.Equal(got, PlayerColors()[:size]) {
			t.Fatalf("no claims, %d players: %v", size, got)
		}
		if got := assignColors(eight[:size], json.RawMessage(`{"colors":{}}`)); !slices.Equal(got, PlayerColors()[:size]) {
			t.Fatalf("empty claims, %d players: %v", size, got)
		}
	}
	// The earlier seat keeps a doubly claimed color; a claim of a player not
	// in the roster or of an unknown color is ignored.
	got := assignColors(eight, json.RawMessage(`{"colors":{"p3":"mint","p2":"mint","p8":"coral","p5":"red","x":"blue"}}`))
	want := []Color{ColorBlue, ColorMint, ColorGold, ColorViolet, ColorOrange, ColorPink, ColorTeal, ColorCoral}
	if !slices.Equal(got, want) {
		t.Fatalf("claims: %v, want %v", got, want)
	}
	// Eight players, eight different claims: everyone gets their own.
	reversed := slices.Clone(PlayerColors())
	slices.Reverse(reversed)
	claims := map[string]Color{}
	for seat, id := range eight {
		claims[id] = reversed[seat]
	}
	state, _ := json.Marshal(lobbyColors{Colors: claims})
	if got := assignColors(eight, state); !slices.Equal(got, reversed) {
		t.Fatalf("eight claims: %v", got)
	}
	if got := assignColors([]string{"a", "b"}, json.RawMessage(`not json`)); !slices.Equal(got, []Color{ColorMint, ColorCoral}) {
		t.Fatalf("unreadable state: %v", got)
	}
}

func TestColorValid(t *testing.T) {
	for _, color := range PlayerColors() {
		if !color.Valid() {
			t.Fatalf("%s is a catalog color", color)
		}
	}
	for _, color := range []Color{"", "red", "plum", "Mint", " mint"} {
		if color.Valid() {
			t.Fatalf("%q accepted", color)
		}
	}
}
