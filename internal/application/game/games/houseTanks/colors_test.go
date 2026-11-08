package houseTanks

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
		{"first claim", ``, "a", ColorPlum, `{"colors":{"a":"plum"}}`},
		{"someone else's color", `{"colors":{"a":"plum"}}`, "b", ColorPlum, `{"colors":{"a":"plum"}}`},
		{"own color again", `{"colors":{"a":"plum"}}`, "a", ColorPlum, `{"colors":{"a":"plum"}}`},
		{"switch frees the old color", `{"colors":{"a":"plum"}}`, "a", ColorSky, `{"colors":{"a":"sky"}}`},
		{"the freed color is claimable", `{"colors":{"a":"sky"}}`, "b", ColorPlum, `{"colors":{"a":"sky","b":"plum"}}`},
		{"a leaver's color is free", `{"colors":{"c":"plum","d":"ivory"}}`, "a", ColorPlum, `{"colors":{"a":"plum"}}`},
		{"unreadable claims are dropped", `{"colors":{"b":"forest","":"plum"}}`, "a", ColorPlum, `{"colors":{"a":"plum"}}`},
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
	for _, other := range []string{PressMessageType, RoundReadyMessageType, "fausSwitch.selectColor"} {
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
	for _, payload := range []string{`{"color":"red"}`, `{"color":"forest"}`, `{"color":"sky","x":1}`, `{"color":null}`, `{"color":"sky","color":"plum"}`, `{}`, `[]`, `"sky"`} {
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
	got := assignColors(eight, json.RawMessage(`{"colors":{"p3":"plum","p2":"plum","p8":"orange","p5":"red","x":"blue"}}`))
	want := []Color{ColorBlue, ColorPlum, ColorOlive, ColorMustard, ColorCrimson, ColorSky, ColorIvory, ColorOrange}
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
	if got := assignColors([]string{"a", "b"}, json.RawMessage(`not json`)); !slices.Equal(got, []Color{ColorPlum, ColorOrange}) {
		t.Fatalf("unreadable state: %v", got)
	}
}

func TestRosterWithColors(t *testing.T) {
	plain := []RuntimePlayer{{PlayerID: "a"}, {PlayerID: "b"}, {PlayerID: "c"}}
	roster, ok := rosterWithColors(plain)
	if !ok || roster[0].Color != ColorPlum || roster[1].Color != ColorOrange || roster[2].Color != ColorBlue || plain[0].Color != "" {
		t.Fatalf("a roster without colors takes the seat colors on a copy: %+v %v", roster, ok)
	}
	colored := []RuntimePlayer{{PlayerID: "a", Color: ColorSky}, {PlayerID: "b", Color: ColorPlum}}
	if roster, ok := rosterWithColors(colored); !ok || !slices.Equal(roster, colored) {
		t.Fatalf("colors are kept: %+v", roster)
	}
	for name, roster := range map[string][]RuntimePlayer{
		"mixed":     {{PlayerID: "a", Color: ColorSky}, {PlayerID: "b"}},
		"duplicate": {{PlayerID: "a", Color: ColorSky}, {PlayerID: "b", Color: ColorSky}},
		"unknown":   {{PlayerID: "a", Color: ColorSky}, {PlayerID: "b", Color: "forest"}},
	} {
		if _, ok := rosterWithColors(roster); ok {
			t.Fatalf("%s roster accepted", name)
		}
	}
}
