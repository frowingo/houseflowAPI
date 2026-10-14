// Package module is the contract between the shared multiplayer core and a
// game. The core owns sessions, room ownership, persistence, transport and
// fanout; a module owns its rules, wire models and simulation.
package module

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

// End reasons the core itself can request from any runtime.
const (
	EndCancelledByUser = "cancelledByUser"
	EndRecoveryFailed  = "recoveryFailed"
)

// Player is a frozen roster entry handed to a runtime.
type Player struct {
	PlayerID    string
	DisplayName string
}

// ResultCodec owns the payload schema of a game's immutable match result.
type ResultCodec interface {
	// CancellationResult reconstructs a cancelled result from the roster when
	// no runtime state survives. It never invents a winner.
	CancellationResult(session gameDomain.SessionSnapshot, reason string, now time.Time) (gameDomain.MatchResult, error)
	// FinalizeResult validates a proposed result against the stored session and
	// returns the canonical result to persist.
	FinalizeResult(proposed gameDomain.MatchResult, session gameDomain.SessionSnapshot) (gameDomain.MatchResult, error)
	// DecodeResult checks a stored result and returns its public wire model.
	DecodeResult(stored gameDomain.MatchResult) (any, error)
}

// ErrorCode namespaces a game-specific application error key.
func ErrorCode(gameKey, code string) string { return gameKey + ".error." + code }

// Server and client message types every tick game exposes under its key.
func SnapshotMessageType(gameKey string) string       { return gameKey + ".snapshot" }
func ControlGrantedMessageType(gameKey string) string { return gameKey + ".controlGranted" }
func ResyncMessageType(gameKey string) string         { return gameKey + ".resync" }
func ResultMessageType(gameKey string) string         { return gameKey + ".result" }

var (
	ErrInvalidInput     = errors.New("invalid gameplay input")
	ErrInputExpired     = errors.New("gameplay input has expired")
	ErrStaleInput       = errors.New("input sequence or runtime epoch is stale")
	ErrControlRequired  = errors.New("current controller binding is required")
	ErrPlayerNotFound   = errors.New("player is not part of the runtime")
	ErrPlayerEliminated = errors.New("player was eliminated")
	ErrRuntimeBusy      = errors.New("runtime mailbox is full")
	ErrInputRate        = errors.New("gameplay input rate exceeded")
	ErrFinished         = errors.New("runtime is terminal")
	ErrInvalidConfig    = errors.New("game config is invalid")
)

// Phase is the transient presentation state of a tick runtime, independent of
// the durable session state.
type Phase string

const (
	PhaseCountdown  Phase = "countdown"
	PhasePlaying    Phase = "playing"
	PhaseRecovering Phase = "recovering"
	PhaseFinalizing Phase = "finalizing"
	PhaseEnded      Phase = "ended"
	PhaseCancelled  Phase = "cancelled"
)

// Control input kinds the core sends to every tick runtime.
const (
	InputBind       = "bind"
	InputHeartbeat  = "heartbeat"
	InputDisconnect = "disconnect"
	InputSnapshot   = "snapshot"
)

// ControlInput is the identity part of a cross-instance gameplay input. The
// trusted gateway fills it; game intents extend it with their own fields in the
// same flat JSON object.
type ControlInput struct {
	MessageID         string    `json:"messageId,omitempty"`
	Kind              string    `json:"kind"`
	PlayerID          string    `json:"playerId"`
	ConnectionID      string    `json:"connectionId"`
	RuntimeEpoch      int64     `json:"runtimeEpoch"`
	ControlGeneration string    `json:"controlGeneration"`
	InputSequence     int64     `json:"inputSequence"`
	CreatedAt         time.Time `json:"createdAt"`
}

// ControlGrant binds one connection to a player's controls for one epoch.
type ControlGrant struct {
	SessionID         string `json:"sessionId"`
	PlayerID          string `json:"playerId"`
	RuntimeEpoch      int64  `json:"runtimeEpoch"`
	ControlGeneration string `json:"controlGeneration"`
}

// Intent is a strictly validated client gameplay message. The gateway adds the
// authenticated control identity before it is sent to the runtime.
type Intent interface {
	RuntimeInput(control ControlInput) any
}

// FrameHeader is the part of every published tick frame that the transport
// reads; the rest of the frame is opaque to the core.
type FrameHeader struct {
	RuntimeEpoch  int64 `json:"runtimeEpoch"`
	StateSequence int64 `json:"stateSequence"`
	Phase         Phase `json:"phase"`
	World         struct {
		SessionID string        `json:"sessionId"`
		Players   []FramePlayer `json:"players"`
	} `json:"world"`
	Controls []FrameControl `json:"controls"`
}

type FramePlayer struct {
	PlayerID string `json:"playerId"`
	IsAlive  bool   `json:"isAlive"`
}

type FrameControl struct {
	PlayerID     string `json:"playerId"`
	ConnectionID string `json:"connectionId"`
}

// Alive reports whether the player is still in play in this frame.
func (header FrameHeader) Alive(playerID string) bool {
	for _, player := range header.World.Players {
		if player.PlayerID == playerID {
			return player.IsAlive
		}
	}
	return false
}

// ControlledBy reports whether the connection currently steers the live player.
func (header FrameHeader) ControlledBy(playerID, connectionID string) bool {
	if header.Phase != PhasePlaying || !header.Alive(playerID) {
		return false
	}
	for _, control := range header.Controls {
		if control.PlayerID == playerID {
			return control.ConnectionID == connectionID
		}
	}
	return false
}

// Frame is one authoritative runtime state published to every connection.
type Frame struct {
	Sequence   int64
	Payload    json.RawMessage
	Checkpoint *Checkpoint
}

// Checkpoint is transient recovery state. State is encoded only when the core
// decides to store it; CriticalKey changes when an uncheckpointed loss would
// be visible (control handover, elimination, terminal proposal).
type Checkpoint struct {
	Epoch       int64
	Sequence    int64
	CapturedAt  time.Time
	CriticalKey string
	State       any
}

// Update is one runtime publication: a frame, a connection-targeted control
// grant, or a connection-targeted rejection.
type Update struct {
	MessageID    string
	ConnectionID string
	Frame        *Frame
	Grant        *ControlGrant
	Err          error
	Checkpoint   *Checkpoint
}

// Viewer is the connection a frame is projected for.
type Viewer struct {
	PlayerID     string
	ConnectionID string
	Grant        *ControlGrant
}

// TickTiming exposes the runtime limits the transport enforces before input
// reaches the runtime.
type TickTiming struct {
	// ControlTimeout also bounds the lobby heartbeat of a ready player.
	ControlTimeout    time.Duration
	InputLifetime     time.Duration
	MaximumIntentRate int
}

type RuntimeParams struct {
	SessionID string
	HouseID   string
	StartedAt time.Time
	Players   []Player
	Epoch     int64
	Now       time.Time
	// InitialSequence continues countdown preview frame numbering.
	InitialSequence int64
	Settings        json.RawMessage
}

type RestoreParams struct {
	Session gameDomain.SessionSnapshot
	Epoch   int64
	Now     time.Time
	// Stored is the checkpoint header kept beside the payload; both must agree.
	Stored Checkpoint
	// CancelReason, when set, ends a restored match that has no outcome yet;
	// a terminal proposal is immutable and kept.
	CancelReason string
}

type PreviewParams struct {
	SessionID       string
	HouseID         string
	CountdownEndsAt time.Time
	Players         []Player
	Epoch           int64
	Sequence        int64
}

// Config is the operator-tunable part of a game, kept in the Game collection.
// Rules start from Definition().Rules; Settings is the module's own JSON
// object. A resolved config is frozen into each session at creation, so a
// change never reaches a session that already exists.
type Config struct {
	Rules    gameDomain.SessionRules
	Settings json.RawMessage
}

// Game is what every module provides, whatever lane runs it.
type Game interface {
	ResultCodec
	Definition() gameDomain.GameDefinition
	// ResolveConfig overlays operator settings on the module defaults, checks
	// rules and settings against the module's hard limits and returns the
	// canonical config. Contract values (physics, course, protocol) are never
	// configurable.
	ResolveConfig(override Config) (Config, error)
	// ClientSettings is the public view of resolved settings a client needs
	// before it connects.
	ClientSettings(settings json.RawMessage) any
}

// TickModule is a server-authoritative realtime game advanced by a fixed-rate
// scheduler. Its runtime never performs database, Redis or socket I/O.
// Settings arguments are the session's frozen settings; empty means defaults.
type TickModule interface {
	Game
	Timing(settings json.RawMessage) TickTiming
	Welcome(sessionID, connectionID string, settings json.RawMessage) any
	// DecodeIntent strictly decodes a client gameplay message type the module
	// owns; unknown types return ErrUnsupportedIntent.
	DecodeIntent(messageType string, payload json.RawMessage) (Intent, error)
	NewRuntime(params RuntimeParams) (TickRuntime, error)
	// RestoreRuntime validates a stored checkpoint against the session and
	// resumes it under a newer epoch with physics frozen and the session's
	// settings.
	RestoreRuntime(checkpoint json.RawMessage, params RestoreParams) (TickRuntime, error)
	PreviewFrame(params PreviewParams) (Frame, error)
	// PrepareFrame decodes a published frame once per instance; the view then
	// renders it for every connection of the room.
	PrepareFrame(payload json.RawMessage) (FrameView, error)
}

// FrameView renders one decoded frame as public snapshots. Viewers with the
// same ViewKey receive byte-identical snapshots, so the core renders once per
// key and shares the bytes. Render returns compact JSON, which the core embeds
// without re-encoding. A view is used by one goroutine at a time.
type FrameView interface {
	Header() FrameHeader
	ViewKey(viewer Viewer) string
	Render(viewer Viewer) (json.RawMessage, error)
}

var ErrUnsupportedIntent = errors.New("unsupported gameplay message type")

// TickRuntime is one running match. Submit only writes bounded mailboxes; Run
// owns advancement and no callback runs under the runtime's lock.
type TickRuntime interface {
	Submit(input json.RawMessage, now time.Time) error
	Run(ctx context.Context, leaseValid func() bool)
	Advance(now time.Time) error
	Cancel(reason string) error
	Reconcile(playingIDs, memberIDs []string)
	// NextUpdate blocks until the runtime publishes or ctx ends.
	NextUpdate(ctx context.Context) (Update, error)
	// TakeResult returns the proposed terminal result once, without blocking.
	TakeResult() (gameDomain.MatchResult, bool, error)
	// CommittedFrame is requested only after the result transaction commits.
	CommittedFrame(result gameDomain.MatchResult) (Frame, error)
	Checkpoint() Checkpoint
	// RecoveryFrame advertises restored state while controls rebind.
	RecoveryFrame() (Frame, error)
	NotifyRejected(connectionID, messageID string, err error)
	SetObserver(observer RuntimeObserver)
}

// RuntimeObserver is called outside the physics mutex. Implementations must be
// non-blocking and must not perform storage, network I/O or log per tick.
type RuntimeObserver interface{ ObserveRuntime(RuntimeObservation) }

// RuntimeObservation contains bounded server-side samples, never player
// identities. Input age starts at authenticated server ingress, not mobile RTT.
type RuntimeObservation struct {
	StepGap         time.Duration
	StepDuration    time.Duration
	InputAges       []time.Duration
	FramesCoalesced uint64
	InputsCoalesced uint64
	Overloaded      bool
}
