package houseRockets

import (
	"encoding/json"

	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
)

// Rocket colors. In the lobby each player may claim a color with
// houseRockets.selectColor; the first claim of a color wins. The claims are
// the session's lobby state, {"colors": {"<playerId>": "<color>"}}, which
// every sessionSnapshot carries so the lobby can show them (ADR-030, as in
// Faus Switch and FauseTanks). When the countdown starts, assignColors gives
// every rocket its final color; it is part of the simulation state, so the
// checkpoint keeps it for the whole match.

// lobbyColors is the session's lobby state.
type lobbyColors struct {
	Colors map[string]Color `json:"colors"`
}

// readLobbyColors reads a lobby state leniently: anything unreadable is no
// claim, and claims of unknown colors are dropped.
func readLobbyColors(state json.RawMessage) lobbyColors {
	var lobby lobbyColors
	if len(state) > 0 {
		_ = json.Unmarshal(state, &lobby)
	}
	claims := make(map[string]Color, len(lobby.Colors))
	for playerID, color := range lobby.Colors {
		if playerID != "" && color.Valid() {
			claims[playerID] = color
		}
	}
	return lobbyColors{Colors: claims}
}

// DecodeLobbyIntent is the module's LobbyModule.DecodeLobbyIntent: the only
// lobby intent is houseRockets.selectColor.
func DecodeLobbyIntent(messageType string, payload json.RawMessage) (json.RawMessage, error) {
	if messageType != SelectColorMessageType {
		return nil, module.ErrUnsupportedIntent
	}
	selection, err := DecodeSelectColor(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(selection)
}

// ApplyLobbyIntent is the module's LobbyModule.ApplyLobbyIntent. Claims of
// players who are no longer in the session are dropped first, so a leaver's
// color is free again. A color nobody else holds becomes the player's (and
// frees the player's previous one); a color someone else holds changes
// nothing, and the intent is still accepted.
func ApplyLobbyIntent(session gameDomain.SessionSnapshot, playerID, messageType string, intent json.RawMessage) (json.RawMessage, error) {
	if messageType != SelectColorMessageType {
		return nil, module.ErrUnsupportedIntent
	}
	selection, err := DecodeSelectColor(intent)
	if err != nil {
		return nil, err
	}
	lobby := readLobbyColors(session.LobbyState)
	joined := make(map[string]bool, len(session.Players))
	for _, player := range session.Players {
		joined[player.PlayerID] = player.State != gameDomain.PlayerLeft
	}
	holder := ""
	for claimant, color := range lobby.Colors {
		switch {
		case !joined[claimant]:
			delete(lobby.Colors, claimant)
		case color == selection.Color:
			holder = claimant
		}
	}
	if holder == "" || holder == playerID {
		lobby.Colors[playerID] = selection.Color
	}
	return json.Marshal(lobby)
}

// assignColors gives every rocket of a match its color, by seat (join order):
// a player keeps the color they claimed (if two claim the same color, the
// earlier seat keeps it); everyone else gets the first free color in catalog
// order. Without claims this is the catalog in seat order, as before lobby
// colors. PreviewFrame and NewRuntime both call it (through NewSimulation)
// with the frozen roster and the session's lobby state, so they agree.
func assignColors(playerIDs []string, lobbyState json.RawMessage) []Color {
	claims := readLobbyColors(lobbyState).Colors
	colors := make([]Color, len(playerIDs))
	taken := make(map[Color]bool, len(playerIDs))
	for seat, playerID := range playerIDs {
		if color, claimed := claims[playerID]; claimed && !taken[color] {
			colors[seat], taken[color] = color, true
		}
	}
	free := PlayerColors()
	for seat := range colors {
		if colors[seat] != "" {
			continue
		}
		for len(free) > 0 && taken[free[0]] {
			free = free[1:]
		}
		if len(free) > 0 {
			colors[seat], taken[free[0]] = free[0], true
		}
	}
	return colors
}
