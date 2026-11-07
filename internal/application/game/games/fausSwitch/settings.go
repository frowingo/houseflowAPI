package fausSwitch

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
)

// Operator limits for the lobby rules.
const (
	minimumReadyWindow = 5 * time.Second
	maximumReadyWindow = 2 * time.Minute
	minimumCountdown   = time.Second
	maximumCountdown   = 10 * time.Second
)

var ErrInvalidSettings = errors.Join(module.ErrInvalidConfig, errors.New("faus switch settings are out of range"))

// Settings are the operator-tunable network and presence limits of a race.
// Physics rate, rewind window and match length are contract values: the
// simulation, checkpoints and results depend on them, so they stay in code.
type Settings struct {
	SnapshotRateHz               int   `json:"snapshotRateHz"`
	MaximumInputRateHz           int   `json:"maximumInputRateHz"`
	ControlHeartbeatMilliseconds int64 `json:"controlHeartbeatMilliseconds"`
	ControlTimeoutMilliseconds   int64 `json:"controlTimeoutMilliseconds"`
	ReconnectGraceMilliseconds   int64 `json:"reconnectGraceMilliseconds"`
}

func DefaultSettings() Settings {
	return Settings{
		SnapshotRateHz:               SnapshotRateHz,
		MaximumInputRateHz:           MaximumInputRateHz,
		ControlHeartbeatMilliseconds: ControlHeartbeatInterval.Milliseconds(),
		ControlTimeoutMilliseconds:   ControlTimeout.Milliseconds(),
		ReconnectGraceMilliseconds:   ReconnectGraceDuration.Milliseconds(),
	}
}

// Validate keeps snapshots on scheduler steps, keeps the input rate within
// what one scheduler step can merge, and leaves at least two missed
// heartbeats before control expires.
func (settings Settings) Validate() error {
	valid := settings.SnapshotRateHz >= 5 && settings.SnapshotRateHz <= SchedulingRateHz &&
		settings.MaximumInputRateHz >= 1 && settings.MaximumInputRateHz <= SchedulingRateHz &&
		settings.ControlHeartbeatMilliseconds >= 500 && settings.ControlHeartbeatMilliseconds <= 10_000 &&
		settings.ControlTimeoutMilliseconds >= 2*settings.ControlHeartbeatMilliseconds && settings.ControlTimeoutMilliseconds <= 30_000 &&
		settings.ReconnectGraceMilliseconds >= 0 && settings.ReconnectGraceMilliseconds <= 60_000
	if !valid {
		return ErrInvalidSettings
	}
	return nil
}

func (settings Settings) clientModel() FausSwitchRuntimeSettingsModel {
	return FausSwitchRuntimeSettingsModel{
		PhysicsRateHz:                PhysicsRateHz,
		SnapshotRateHz:               settings.SnapshotRateHz,
		MaximumInputRateHz:           settings.MaximumInputRateHz,
		MaximumRewindMilliseconds:    MaximumRewindDuration.Milliseconds(),
		ControlHeartbeatMilliseconds: settings.ControlHeartbeatMilliseconds,
		ControlTimeoutMilliseconds:   settings.ControlTimeoutMilliseconds,
		ReconnectGraceMilliseconds:   settings.ReconnectGraceMilliseconds,
		MaximumMatchMilliseconds:     MaximumMatchDuration.Milliseconds(),
	}
}

// ResolveConfig overlays operator settings on the defaults and checks the
// lobby rules; the result is frozen into every new session.
func ResolveConfig(override module.Config) (module.Config, error) {
	if !validRules(override) {
		return module.Config{}, ErrInvalidSettings
	}
	settings, err := resolveSettings(override.Settings)
	if err != nil {
		return module.Config{}, err
	}
	encoded, err := json.Marshal(settings)
	return module.Config{Rules: override.Rules, Settings: encoded}, err
}

// RuntimeSettings is the client view of a session's frozen settings (the
// catalog and the welcome); anything that no longer resolves falls back to
// the defaults.
func RuntimeSettings(frozen json.RawMessage) FausSwitchRuntimeSettingsModel {
	return sessionSettings(frozen).clientModel()
}

// resolveSettings rejects unknown keys so a typo cannot silently keep a default.
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

func sessionSettings(frozen json.RawMessage) Settings {
	settings, err := resolveSettings(frozen)
	if err != nil {
		return DefaultSettings()
	}
	return settings
}

func validRules(config module.Config) bool {
	limits := config.Rules
	return limits.Validate() == nil && limits.MinimumPlayers >= MinimumPlayers && limits.MaximumPlayers <= MaximumPlayers &&
		limits.ReadyWindowDuration >= minimumReadyWindow && limits.ReadyWindowDuration <= maximumReadyWindow &&
		limits.CountdownDuration >= minimumCountdown && limits.CountdownDuration <= maximumCountdown &&
		limits.ReadyWindowDuration%time.Millisecond == 0 && limits.CountdownDuration%time.Millisecond == 0
}
