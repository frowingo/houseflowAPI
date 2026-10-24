package houseTanks

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
)

// Operator limits for the lobby rules. The roster is bounded by the player
// colors and the largest map; the countdown carries the preview frames.
const (
	minimumReadyWindow = 5 * time.Second
	maximumReadyWindow = 2 * time.Minute
	minimumCountdown   = time.Second
	maximumCountdown   = 10 * time.Second
)

var ErrInvalidSettings = errors.Join(module.ErrInvalidConfig, errors.New("house tanks settings are out of range"))

// Settings are the operator-tunable network, presence and lag-compensation
// limits of a match. Physics, maps and match rules are contract values:
// checkpoints and results are validated against them, so they stay in code.
type Settings struct {
	SnapshotRateHz                int   `json:"snapshotRateHz"`
	MaximumInputRateHz            int   `json:"maximumInputRateHz"`
	MaximumPressMessagesPerSecond int   `json:"maximumPressMessagesPerSecond"`
	ControlHeartbeatMilliseconds  int64 `json:"controlHeartbeatMilliseconds"`
	ControlTimeoutMilliseconds    int64 `json:"controlTimeoutMilliseconds"`
	ReconnectGraceMilliseconds    int64 `json:"reconnectGraceMilliseconds"`
	LagCompensationMilliseconds   int64 `json:"lagCompensationMilliseconds"`
	HeadingToleranceMilliradians  int64 `json:"headingToleranceMilliradians"`
}

func DefaultSettings() Settings {
	return Settings{
		SnapshotRateHz:                SnapshotRateHz,
		MaximumInputRateHz:            MaximumInputRateHz,
		MaximumPressMessagesPerSecond: MaximumPressMessagesPerSecond,
		ControlHeartbeatMilliseconds:  ControlHeartbeatInterval.Milliseconds(),
		ControlTimeoutMilliseconds:    ControlTimeout.Milliseconds(),
		ReconnectGraceMilliseconds:    ReconnectGraceDuration.Milliseconds(),
		LagCompensationMilliseconds:   LagCompensationWindow.Milliseconds(),
		HeadingToleranceMilliradians:  HeadingToleranceMilliradians,
	}
}

// Validate keeps frames on scheduler ticks, lets a client send its advertised
// input rate, leaves at least two missed heartbeats before control expires and
// keeps lag compensation inside the input lifetime.
func (settings Settings) Validate() error {
	valid := settings.SnapshotRateHz >= 5 && settings.SnapshotRateHz <= SchedulingRateHz &&
		settings.MaximumPressMessagesPerSecond >= 1 && settings.MaximumPressMessagesPerSecond <= SchedulingRateHz &&
		settings.MaximumInputRateHz >= 1 && settings.MaximumInputRateHz <= settings.MaximumPressMessagesPerSecond &&
		settings.ControlHeartbeatMilliseconds >= 500 && settings.ControlHeartbeatMilliseconds <= 10_000 &&
		settings.ControlTimeoutMilliseconds >= 2*settings.ControlHeartbeatMilliseconds && settings.ControlTimeoutMilliseconds <= 30_000 &&
		settings.ReconnectGraceMilliseconds >= 0 && settings.ReconnectGraceMilliseconds <= 60_000 &&
		settings.LagCompensationMilliseconds >= 0 && settings.LagCompensationMilliseconds <= 250 &&
		settings.HeadingToleranceMilliradians >= 0 && settings.HeadingToleranceMilliradians <= 200
	if !valid {
		return ErrInvalidSettings
	}
	return nil
}

func (settings Settings) controlTimeout() time.Duration {
	return time.Duration(settings.ControlTimeoutMilliseconds) * time.Millisecond
}

func (settings Settings) clientModel() HouseTanksRuntimeSettingsModel {
	return HouseTanksRuntimeSettingsModel{
		PhysicsRateHz:                 PhysicsRateHz,
		SchedulingRateHz:              SchedulingRateHz,
		SnapshotRateHz:                settings.SnapshotRateHz,
		MaximumInputRateHz:            settings.MaximumInputRateHz,
		MaximumPressMessagesPerSecond: settings.MaximumPressMessagesPerSecond,
		ControlHeartbeatMilliseconds:  settings.ControlHeartbeatMilliseconds,
		ControlTimeoutMilliseconds:    settings.ControlTimeoutMilliseconds,
		ReconnectGraceMilliseconds:    settings.ReconnectGraceMilliseconds,
		LagCompensationMilliseconds:   settings.LagCompensationMilliseconds,
		HeadingToleranceMilliradians:  settings.HeadingToleranceMilliradians,
		ArenaWidth:                    ArenaWidth,
		ArenaHeight:                   ArenaHeight,
		RoundWinsNeeded:               RoundWinsNeeded,
		ArmorPerRound:                 ArmorPerRound,
		RoundCountdownMilliseconds:    RoundCountdownDuration.Milliseconds(),
		RoundReadyWindowMilliseconds:  RoundReadyWindowDuration.Milliseconds(),
		RoundReadyIdleMilliseconds:    RoundReadyIdleLimit.Milliseconds(),
		MaximumMatchMilliseconds:      MaximumMatchDuration.Milliseconds(),
		EventWindowMilliseconds:       EventWindow.Milliseconds(),
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

// sessionSettings reads the settings frozen into a session; anything that no
// longer resolves falls back to the defaults.
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
