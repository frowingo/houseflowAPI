package fausSwitch

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

func selectColor(t *testing.T, session gameDomain.SessionSnapshot, playerID string, color Color) string {
	t.Helper()
	state, err := ApplyLobbyIntent(session, playerID, SelectColorMessageType, json.RawMessage(`{"color":"`+string(color)+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	return string(state)
}

func TestColorClaims(t *testing.T) {
	waiting := map[string]gameDomain.PlayerState{"a": gameDomain.PlayerWaiting, "b": gameDomain.PlayerReady}
	for _, step := range []struct {
		name, state, player string
		color               Color
		want                string
	}{
		{"first claim", ``, "a", ColorPlum, `{"colors":{"a":"plum"}}`},
		{"someone else's color", `{"colors":{"a":"plum"}}`, "b", ColorPlum, `{"colors":{"a":"plum"}}`},
		{"own color again", `{"colors":{"a":"plum"}}`, "a", ColorPlum, `{"colors":{"a":"plum"}}`},
		{"switch frees the old color", `{"colors":{"a":"plum"}}`, "a", ColorOcean, `{"colors":{"a":"ocean"}}`},
		{"a leaver's color is free", `{"colors":{"c":"plum","d":"berry"}}`, "a", ColorPlum, `{"colors":{"a":"plum"}}`},
		{"unreadable state is no claim", `{"colors":{"b":"red","":"plum"}}`, "a", ColorPlum, `{"colors":{"a":"plum"}}`},
	} {
		players := map[string]gameDomain.PlayerState{"a": waiting["a"], "b": waiting["b"], "c": gameDomain.PlayerLeft}
		if got := selectColor(t, lobbyOf(step.state, players), step.player, step.color); got != step.want {
			t.Fatalf("%s: %s, want %s", step.name, got, step.want)
		}
	}
}

func TestLobbyIntentDecoding(t *testing.T) {
	// The core probes the type with no payload.
	if _, err := DecodeLobbyIntent(FlipMessageType, nil); !errors.Is(err, module.ErrUnsupportedIntent) {
		t.Fatalf("flip: %v", err)
	}
	if _, err := ApplyLobbyIntent(gameDomain.SessionSnapshot{}, "a", FlipMessageType, nil); !errors.Is(err, module.ErrUnsupportedIntent) {
		t.Fatalf("apply flip: %v", err)
	}
	if _, err := DecodeLobbyIntent(SelectColorMessageType, nil); err == nil || errors.Is(err, module.ErrUnsupportedIntent) {
		t.Fatalf("selectColor without payload: %v", err)
	}
	for _, payload := range []string{`{"color":"red"}`, `{"color":"plum","x":1}`, `{}`, `[]`} {
		if _, err := DecodeLobbyIntent(SelectColorMessageType, json.RawMessage(payload)); !errors.Is(err, module.ErrInvalidInput) {
			t.Fatalf("%s: %v", payload, err)
		}
	}
	if intent, err := DecodeLobbyIntent(SelectColorMessageType, json.RawMessage(`{ "color" : "berry" }`)); err != nil || string(intent) != `{"color":"berry"}` {
		t.Fatalf("canonical intent: %s %v", intent, err)
	}
}

func TestAssignColors(t *testing.T) {
	eight := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"}
	if got := assignColors(eight, nil); !slices.Equal(got, PlayerColors()) {
		t.Fatalf("no claims: %v", got)
	}
	got := assignColors(eight, json.RawMessage(`{"colors":{"p3":"plum","p2":"plum","p8":"forest","p5":"red","x":"ocean"}}`))
	want := []Color{ColorOcean, ColorPlum, ColorLavender, ColorOlive, ColorBronze, ColorGraphite, ColorBerry, ColorForest}
	if !slices.Equal(got, want) {
		t.Fatalf("claims: %v, want %v", got, want)
	}
	seen := map[Color]bool{}
	for _, color := range got {
		if seen[color] || !color.Valid() {
			t.Fatalf("colors repeat: %v", got)
		}
		seen[color] = true
	}
	if got := assignColors([]string{"a", "b"}, json.RawMessage(`not json`)); !slices.Equal(got, []Color{ColorPlum, ColorForest}) {
		t.Fatalf("unreadable state: %v", got)
	}
}
