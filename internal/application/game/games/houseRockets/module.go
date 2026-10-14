package houseRockets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
)

// Module exposes House Rockets to the shared multiplayer core.
func Module() module.TickModule { return gameModule{} }

type gameModule struct{}

var _ module.TickModule = gameModule{}

func (gameModule) Definition() gameDomain.GameDefinition { return Definition() }

func (gameModule) ResolveConfig(override module.Config) (module.Config, error) {
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

func (gameModule) ClientSettings(settings json.RawMessage) any {
	return sessionSettings(settings).clientModel()
}

func (gameModule) Timing(settings json.RawMessage) module.TickTiming {
	resolved := sessionSettings(settings)
	return module.TickTiming{ControlTimeout: resolved.controlTimeout(), InputLifetime: InputLifetime, MaximumIntentRate: resolved.MaximumSteerMessagesPerSecond}
}

func (gameModule) Welcome(sessionID, connectionID string, settings json.RawMessage) any {
	return HouseRocketsWelcomeModel{SessionID: sessionID, GameKey: GameKey, ConnectionID: connectionID, ProtocolVersion: ProtocolVersion, CourseVersion: CourseVersion, Settings: sessionSettings(settings).clientModel()}
}

func (gameModule) DecodeIntent(messageType string, payload json.RawMessage) (module.Intent, error) {
	if messageType != SteerMessageType {
		return nil, module.ErrUnsupportedIntent
	}
	var steer HouseRocketsSteerModel
	if err := helpers.DecodeStrictObject(payload, &steer, nil, "controlGeneration", "inputSequence", "heading"); err != nil {
		return nil, errors.Join(ErrInvalidInput, err)
	}
	if !steer.Valid() {
		return nil, ErrInvalidInput
	}
	steer.Heading = math.Atan2(math.Sin(steer.Heading), math.Cos(steer.Heading))
	return steerIntent{steer}, nil
}

type steerIntent struct{ HouseRocketsSteerModel }

func (intent steerIntent) RuntimeInput(control module.ControlInput) any {
	return RuntimeInput{MessageID: control.MessageID, Kind: InputSteer, PlayerID: control.PlayerID, ConnectionID: control.ConnectionID, RuntimeEpoch: control.RuntimeEpoch,
		ControlGeneration: intent.ControlGeneration, InputSequence: intent.InputSequence, Heading: intent.Heading, CreatedAt: control.CreatedAt}
}

func (gameModule) NewRuntime(params module.RuntimeParams) (module.TickRuntime, error) {
	runtime, err := newRuntime(sessionSettings(params.Settings), NewSimulationParams{SessionID: params.SessionID, HouseID: params.HouseID, StartedAt: params.StartedAt, Players: params.Players}, params.Epoch, params.Now, params.InitialSequence)
	if err != nil {
		return nil, err
	}
	return tickRuntime{runtime}, nil
}

func (gameModule) RestoreRuntime(payload json.RawMessage, params module.RestoreParams) (module.TickRuntime, error) {
	var checkpoint RuntimeCheckpoint
	if err := json.Unmarshal(payload, &checkpoint); err != nil {
		return nil, errors.Join(ErrInvalidSimulationState, err)
	}
	if !checkpointMatches(checkpoint, params) {
		return nil, ErrInvalidSimulationState
	}
	runtime, err := restoreRuntime(sessionSettings(params.Session.Settings), checkpoint, params.Epoch, params.Now)
	if err != nil {
		return nil, err
	}
	if err := ValidateCheckpointSession(checkpoint, params.Session); err != nil {
		return nil, err
	}
	if params.CancelReason != "" && checkpoint.World.Outcome == nil {
		_ = runtime.Cancel(EndReason(params.CancelReason))
	}
	return tickRuntime{runtime}, nil
}

// checkpointMatches binds a stored payload to its header, its session and the
// recovering instance's clock before any state is trusted.
func checkpointMatches(checkpoint RuntimeCheckpoint, params module.RestoreParams) bool {
	stored, session := params.Stored, params.Session
	sameHeader := stored.Epoch == checkpoint.Epoch && stored.Sequence == checkpoint.Sequence && stored.CapturedAt.Equal(checkpoint.CapturedAt)
	sameSession := checkpoint.World.SessionID == session.SessionID && checkpoint.World.HouseID == session.HouseID &&
		checkpoint.World.StartedAt.Truncate(time.Millisecond).Equal(session.StartedAt.Truncate(time.Millisecond))
	return sameHeader && sameSession && checkpoint.CapturedAt.Before(params.Now.Add(time.Second))
}

// wireFrame is the cross-instance frame payload: a runtime frame, plus the
// countdown deadline while previewing.
type wireFrame struct {
	RuntimeFrame
	CountdownEndsAt *time.Time `json:"countdownEndsAt"`
}

func (gameModule) PreviewFrame(params module.PreviewParams) (module.Frame, error) {
	simulation, err := NewSimulation(NewSimulationParams{SessionID: params.SessionID, HouseID: params.HouseID, StartedAt: params.CountdownEndsAt, Players: params.Players})
	if err != nil {
		return module.Frame{}, err
	}
	countdownEndsAt := params.CountdownEndsAt
	frame := wireFrame{RuntimeFrame: RuntimeFrame{RuntimeEpoch: params.Epoch, StateSequence: params.Sequence, Phase: PhaseCountdown, World: simulation.Snapshot(), Controls: []RuntimePlayerControl{}}, CountdownEndsAt: &countdownEndsAt}
	payload, err := json.Marshal(frame)
	return module.Frame{Sequence: params.Sequence, Payload: payload}, err
}

func (gameModule) PrepareFrame(payload json.RawMessage) (module.FrameView, error) {
	view := &frameView{}
	if err := json.Unmarshal(payload, &view.frame); err != nil {
		return nil, err
	}
	return view, nil
}

// frameView renders the public snapshot once. A controlling connection's
// snapshot differs only by its own player's controlGeneration, which is
// spliced into the shared bytes instead of encoding the snapshot again.
type frameView struct {
	frame  wireFrame
	shared json.RawMessage
}

var nullControlGeneration = []byte(`"controlGeneration":null`)

func (view *frameView) Header() module.FrameHeader {
	frame := view.frame.RuntimeFrame
	header := module.FrameHeader{RuntimeEpoch: frame.RuntimeEpoch, StateSequence: frame.StateSequence, Phase: frame.Phase,
		Controls: make([]module.FrameControl, 0, len(frame.Controls))}
	header.World.SessionID = frame.World.SessionID
	header.World.Players = make([]module.FramePlayer, 0, len(frame.World.Players))
	for _, player := range frame.World.Players {
		header.World.Players = append(header.World.Players, module.FramePlayer{PlayerID: player.PlayerID, IsAlive: player.IsAlive})
	}
	for _, control := range frame.Controls {
		header.Controls = append(header.Controls, module.FrameControl{PlayerID: control.PlayerID, ConnectionID: control.ConnectionID})
	}
	return header
}

// controlledIndex is the player whose generation this viewer may see.
func (view *frameView) controlledIndex(viewer module.Viewer) int {
	frame, grant := view.frame.RuntimeFrame, viewer.Grant
	if frame.Phase != PhasePlaying || grant == nil || grant.RuntimeEpoch != frame.RuntimeEpoch || grant.PlayerID != viewer.PlayerID {
		return -1
	}
	for index, player := range frame.World.Players {
		if player.PlayerID != viewer.PlayerID {
			continue
		}
		if player.IsAlive && index < len(frame.Controls) && frame.Controls[index].PlayerID == player.PlayerID && frame.Controls[index].ConnectionID == viewer.ConnectionID && frame.Controls[index].Connected {
			return index
		}
		return -1
	}
	return -1
}

func (view *frameView) ViewKey(viewer module.Viewer) string {
	if view.controlledIndex(viewer) < 0 {
		return ""
	}
	return viewer.PlayerID + "\x00" + viewer.Grant.ControlGeneration
}

func (view *frameView) Render(viewer module.Viewer) (json.RawMessage, error) {
	if view.shared == nil {
		model := SnapshotForConnection(view.frame.RuntimeFrame, "", "", nil)
		model.CountdownEndsAt = view.frame.CountdownEndsAt
		shared, err := json.Marshal(model)
		if err != nil {
			return nil, err
		}
		view.shared = shared
	}
	index := view.controlledIndex(viewer)
	if index < 0 {
		return view.shared, nil
	}
	offset := 0
	for occurrence := 0; ; occurrence++ {
		found := bytes.Index(view.shared[offset:], nullControlGeneration)
		if found < 0 {
			return nil, ErrInvalidSimulationState
		}
		offset += found
		if occurrence == index {
			break
		}
		offset += len(nullControlGeneration)
	}
	generation, err := json.Marshal(viewer.Grant.ControlGeneration)
	if err != nil {
		return nil, err
	}
	valueAt := offset + len(nullControlGeneration) - len("null")
	rendered := make([]byte, 0, len(view.shared)+len(generation))
	rendered = append(rendered, view.shared[:valueAt]...)
	rendered = append(rendered, generation...)
	return append(rendered, view.shared[valueAt+len("null"):]...), nil
}

func (gameModule) CancellationResult(session gameDomain.SessionSnapshot, reason string, now time.Time) (gameDomain.MatchResult, error) {
	result, err := RecoveryCancellationResult(session, EndReason(reason), now)
	if err != nil {
		return gameDomain.MatchResult{}, err
	}
	return EncodeMatchResult(result)
}

// FinalizeResult anchors the result to the stored session. BSON dates have
// millisecond precision, so the start comes from the stored session.
func (gameModule) FinalizeResult(proposed gameDomain.MatchResult, session gameDomain.SessionSnapshot) (gameDomain.MatchResult, error) {
	result, err := DecodeMatchResult(proposed)
	if err != nil {
		return gameDomain.MatchResult{}, err
	}
	if err := ValidateResult(result, session); err != nil {
		return gameDomain.MatchResult{}, err
	}
	result.StartedAt = &session.StartedAt
	if result.EndedAt.Before(session.UpdatedAt) {
		result.EndedAt = session.UpdatedAt
	}
	return EncodeMatchResult(result)
}

func (gameModule) DecodeResult(stored gameDomain.MatchResult) (any, error) {
	return DecodeMatchResult(stored)
}

// tickRuntime adapts Runtime's typed channels to the module contract.
type tickRuntime struct{ *Runtime }

func (runtime tickRuntime) Submit(payload json.RawMessage, now time.Time) error {
	var input RuntimeInput
	if err := json.Unmarshal(payload, &input); err != nil {
		return ErrInvalidInput
	}
	return runtime.Runtime.Submit(input, now)
}

func (runtime tickRuntime) Cancel(reason string) error {
	return runtime.Runtime.Cancel(EndReason(reason))
}

func (runtime tickRuntime) NextUpdate(ctx context.Context) (module.Update, error) {
	select {
	case <-ctx.Done():
		return module.Update{}, ctx.Err()
	case frame := <-runtime.Frames():
		published, err := encodeFrame(frame)
		return module.Update{Frame: &published, Checkpoint: published.Checkpoint}, err
	case event := <-runtime.Events():
		update := module.Update{MessageID: event.MessageID, ConnectionID: event.ConnectionID, Grant: event.Grant, Err: event.Err, Checkpoint: encodeCheckpoint(event.Checkpoint)}
		if event.Frame == nil {
			return update, nil
		}
		published, err := encodeFrame(*event.Frame)
		update.Frame, update.Checkpoint = &published, published.Checkpoint
		return update, err
	}
}

func (runtime tickRuntime) TakeResult() (gameDomain.MatchResult, bool, error) {
	select {
	case result := <-runtime.Results():
		stored, err := EncodeMatchResult(result)
		return stored, true, err
	default:
		return gameDomain.MatchResult{}, false, nil
	}
}

func (runtime tickRuntime) CommittedFrame(stored gameDomain.MatchResult) (module.Frame, error) {
	result, err := DecodeMatchResult(stored)
	if err != nil {
		return module.Frame{}, err
	}
	return encodeFrame(runtime.Runtime.CommittedFrame(result))
}

func (runtime tickRuntime) Checkpoint() module.Checkpoint {
	checkpoint := runtime.Runtime.Checkpoint()
	return *encodeCheckpoint(&checkpoint)
}

func (runtime tickRuntime) RecoveryFrame() (module.Frame, error) {
	return encodeFrame(runtime.Runtime.RecoveryFrame())
}

func encodeFrame(frame RuntimeFrame) (module.Frame, error) {
	payload, err := json.Marshal(frame)
	return module.Frame{Sequence: frame.StateSequence, Payload: payload, Checkpoint: encodeCheckpoint(frame.Checkpoint)}, err
}

func encodeCheckpoint(checkpoint *RuntimeCheckpoint) *module.Checkpoint {
	if checkpoint == nil {
		return nil
	}
	return &module.Checkpoint{Epoch: checkpoint.Epoch, Sequence: checkpoint.Sequence, CapturedAt: checkpoint.CapturedAt, CriticalKey: checkpoint.CriticalKey(), State: checkpoint}
}
