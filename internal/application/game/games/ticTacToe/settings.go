package ticTacToe

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
)

var ErrInvalidSettings = errors.Join(module.ErrInvalidConfig, errors.New("tic-tac-toe settings are out of range"))

// Settings are the operator-tunable values; the board and its rules are not.
type Settings struct {
	TurnTimeoutMilliseconds int64 `json:"turnTimeoutMilliseconds"`
}

func DefaultSettings() Settings {
	return Settings{TurnTimeoutMilliseconds: DefaultTurnTimeout.Milliseconds()}
}

func (settings Settings) Validate() error {
	if settings.TurnTimeoutMilliseconds < 5_000 || settings.TurnTimeoutMilliseconds > 300_000 {
		return ErrInvalidSettings
	}
	return nil
}

func (settings Settings) turnTimeout() time.Duration {
	return time.Duration(settings.TurnTimeoutMilliseconds) * time.Millisecond
}

// resolveSettings overlays an operator object on the defaults and rejects
// unknown keys.
func resolveSettings(override json.RawMessage) (Settings, error) {
	settings := DefaultSettings()
	if trimmed := bytes.TrimSpace(override); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return settings, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(override))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, errors.Join(module.ErrInvalidConfig, err)
	}
	if err := helpers.EnsureJSONEnd(decoder); err != nil {
		return Settings{}, errors.Join(module.ErrInvalidConfig, err)
	}
	return settings, settings.Validate()
}

// sessionSettings reads the settings frozen into a session.
func sessionSettings(frozen json.RawMessage) Settings {
	settings, err := resolveSettings(frozen)
	if err != nil {
		return DefaultSettings()
	}
	return settings
}

// validRules allows exactly two players; the lobby timings are tunable.
func validRules(rules module.Config) bool {
	limits := rules.Rules
	return limits.Validate() == nil && limits.MinimumPlayers == Players && limits.MaximumPlayers == Players &&
		limits.ReadyWindowDuration >= 5*time.Second && limits.ReadyWindowDuration <= 2*time.Minute &&
		limits.CountdownDuration >= time.Second && limits.CountdownDuration <= 10*time.Second &&
		limits.ReadyWindowDuration%time.Millisecond == 0 && limits.CountdownDuration%time.Millisecond == 0
}
