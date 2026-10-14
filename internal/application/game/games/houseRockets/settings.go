package houseRockets

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
)

// Operator limits for the lobby rules. The roster is bounded by the player
// colors and the simulation; the countdown carries the preview frames.
const (
	minimumReadyWindow = 5 * time.Second
	maximumReadyWindow = 2 * time.Minute
	minimumCountdown   = time.Second
	maximumCountdown   = 10 * time.Second
)

var ErrInvalidSettings = errors.Join(module.ErrInvalidConfig, errors.New("house rockets settings are out of range"))

// Settings are the operator-tunable network and presence limits of a match.
// Physics, course and match length are contract values: checkpoints and
// results are validated against them, so they stay in code.
type Settings struct {
	SnapshotRateHz                int   `json:"snapshotRateHz"`
	MaximumInputRateHz            int   `json:"maximumInputRateHz"`
	MaximumSteerMessagesPerSecond int   `json:"maximumSteerMessagesPerSecond"`
	ControlHeartbeatMilliseconds  int64 `json:"controlHeartbeatMilliseconds"`
	ControlTimeoutMilliseconds    int64 `json:"controlTimeoutMilliseconds"`
	ReconnectGraceMilliseconds    int64 `json:"reconnectGraceMilliseconds"`
}

func DefaultSettings() Settings {
	return Settings{
		SnapshotRateHz:                SnapshotRateHz,
		MaximumInputRateHz:            MaximumInputRateHz,
		MaximumSteerMessagesPerSecond: MaximumSteerMessagesPerSecond,
		ControlHeartbeatMilliseconds:  ControlHeartbeatInterval.Milliseconds(),
		ControlTimeoutMilliseconds:    ControlTimeout.Milliseconds(),
		ReconnectGraceMilliseconds:    ReconnectGraceDuration.Milliseconds(),
	}
}

// Validate keeps frames on scheduler ticks, lets a client send its advertised
// input rate and leaves at least two missed heartbeats before control expires.
func (settings Settings) Validate() error {
	valid := settings.SnapshotRateHz >= 5 && settings.SnapshotRateHz <= SchedulingRateHz &&
		settings.MaximumSteerMessagesPerSecond >= 1 && settings.MaximumSteerMessagesPerSecond <= SchedulingRateHz &&
		settings.MaximumInputRateHz >= 1 && settings.MaximumInputRateHz <= settings.MaximumSteerMessagesPerSecond &&
		settings.ControlHeartbeatMilliseconds >= 500 && settings.ControlHeartbeatMilliseconds <= 10_000 &&
		settings.ControlTimeoutMilliseconds >= 2*settings.ControlHeartbeatMilliseconds && settings.ControlTimeoutMilliseconds <= 30_000 &&
		settings.ReconnectGraceMilliseconds >= 0 && settings.ReconnectGraceMilliseconds <= 60_000
	if !valid {
		return ErrInvalidSettings
	}
	return nil
}

func (settings Settings) controlTimeout() time.Duration {
	return time.Duration(settings.ControlTimeoutMilliseconds) * time.Millisecond
}

func (settings Settings) reconnectGrace() time.Duration {
	return time.Duration(settings.ReconnectGraceMilliseconds) * time.Millisecond
}

// presenceDeadline is how long a silent controller keeps its player.
func (settings Settings) presenceDeadline() time.Duration {
	return settings.controlTimeout() + settings.reconnectGrace()
}

func (settings Settings) snapshotInterval() time.Duration {
	return time.Second / time.Duration(settings.SnapshotRateHz)
}

func (settings Settings) clientModel() HouseRocketsRuntimeSettingsModel {
	return HouseRocketsRuntimeSettingsModel{
		PhysicsRateHz:                 PhysicsRateHz,
		SchedulingRateHz:              SchedulingRateHz,
		SnapshotRateHz:                settings.SnapshotRateHz,
		MaximumInputRateHz:            settings.MaximumInputRateHz,
		MaximumSteerMessagesPerSecond: settings.MaximumSteerMessagesPerSecond,
		ControlHeartbeatMilliseconds:  settings.ControlHeartbeatMilliseconds,
		ControlTimeoutMilliseconds:    settings.ControlTimeoutMilliseconds,
		ReconnectGraceMilliseconds:    settings.ReconnectGraceMilliseconds,
		MaximumMatchMilliseconds:      MaximumMatchDuration.Milliseconds(),
	}
}

// resolveSettings overlays an operator object on the defaults; unknown keys
// are rejected so a typo cannot silently keep a default.
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

// sessionSettings reads the settings frozen into a session. Sessions created
// before settings were stored, or by the previous release, use the defaults.
func sessionSettings(frozen json.RawMessage) Settings {
	settings, err := resolveSettings(frozen)
	if err != nil {
		return DefaultSettings()
	}
	return settings
}

func validRules(rules module.Config) bool {
	limits := rules.Rules
	return limits.Validate() == nil && limits.MinimumPlayers >= MinimumPlayers && limits.MaximumPlayers <= MaximumPlayers &&
		limits.ReadyWindowDuration >= minimumReadyWindow && limits.ReadyWindowDuration <= maximumReadyWindow &&
		limits.CountdownDuration >= minimumCountdown && limits.CountdownDuration <= maximumCountdown &&
		limits.ReadyWindowDuration%time.Millisecond == 0 && limits.CountdownDuration%time.Millisecond == 0
}
