package houseRockets

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	InputBind        = "bind"
	InputSteer       = "steer"
	InputHeartbeat   = "heartbeat"
	InputDisconnect  = "disconnect"
	InputLifetime    = time.Second
	RuntimeQueueSize = 64
	maximumCatchUp   = 250 * time.Millisecond
)

var (
	ErrControlRequired = errors.New("current controller binding is required")
	ErrStaleInput      = errors.New("input sequence or runtime epoch is stale")
	ErrInputExpired    = errors.New("gameplay input has expired")
	ErrRuntimeBusy     = errors.New("runtime mailbox is full")
	ErrInputRate       = errors.New("steering input rate exceeded")
)

// RuntimeInput comes from a trusted authenticated transport, never directly from
// client-supplied player/connection identities. Epoch and generation are opaque.
type RuntimeInput struct {
	Kind              string    `json:"kind"`
	PlayerID          string    `json:"playerId"`
	ConnectionID      string    `json:"connectionId"`
	RuntimeEpoch      int64     `json:"runtimeEpoch"`
	ControlGeneration string    `json:"controlGeneration"`
	InputSequence     int64     `json:"inputSequence"`
	Heading           float64   `json:"heading"`
	CreatedAt         time.Time `json:"createdAt"`
}

type RuntimePlayerControl struct {
	PlayerID                   string `json:"playerId"`
	Connected                  bool   `json:"connected"`
	LastProcessedInputSequence int64  `json:"lastProcessedInputSequence"`
}

// RuntimeFrame is internal fanout data, not a public wire snapshot. Private
// controller generations are only sent in connection-targeted grants.
type RuntimeFrame struct {
	RuntimeEpoch  int64                  `json:"runtimeEpoch"`
	StateSequence int64                  `json:"stateSequence"`
	Phase         Phase                  `json:"phase"`
	World         WorldSnapshot          `json:"world"`
	Controls      []RuntimePlayerControl `json:"controls"`
}

type RuntimeEvent struct {
	ConnectionID string
	Grant        *HouseRocketsControlGrantedModel
	Err          error
}

type controller struct {
	connectionID   string
	generation     string
	lastSeen       time.Time
	graceEndsAt    time.Time
	highestInput   int64
	processedInput int64
	rateStartedAt  time.Time
	rateCount      int
}

type queuedInput struct {
	input      RuntimeInput
	receivedAt time.Time
}

// Runtime isolates physics from all database, Redis and socket I/O. Run owns
// advancement; Submit only writes bounded mailboxes. Advance is also exposed for
// deterministic scheduler tests. No external callback is invoked under mutex.
type Runtime struct {
	mutex       sync.Mutex
	simulation  *Simulation
	epoch       int64
	controls    map[string]*controller
	pending     map[string]queuedInput
	commands    chan queuedInput
	frames      chan RuntimeFrame
	events      chan RuntimeEvent
	lastAdvance time.Time
	nextFrame   time.Time
	remainder   int64
	sequence    int64
	terminal    bool
	stopped     bool
	runOnce     sync.Once
}

func NewRuntime(params NewSimulationParams, epoch int64, now time.Time) (*Runtime, error) {
	if epoch <= 0 || now.IsZero() {
		return nil, ErrInvalidSimulation
	}
	simulation, err := NewSimulation(params)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{simulation: simulation, epoch: epoch, controls: make(map[string]*controller), pending: make(map[string]queuedInput),
		commands: make(chan queuedInput, RuntimeQueueSize), frames: make(chan RuntimeFrame, 1), events: make(chan RuntimeEvent, RuntimeQueueSize), lastAdvance: now, nextFrame: now.Add(time.Second / SnapshotRateHz)}
	for _, player := range params.Players {
		runtime.controls[player.PlayerID] = &controller{graceEndsAt: now.Add(ControlTimeout + ReconnectGraceDuration)}
	}
	runtime.publishFrame()
	return runtime, nil
}

func (runtime *Runtime) Frames() <-chan RuntimeFrame { return runtime.frames }
func (runtime *Runtime) Events() <-chan RuntimeEvent { return runtime.events }

// Submit confirms mailbox acceptance only. Control grants and snapshot ACKs
// confirm actual processing. Gaps are expected when newer headings coalesce.
func (runtime *Runtime) Submit(input RuntimeInput, now time.Time) error {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	if runtime.stopped || runtime.terminal {
		return ErrSimulationFinished
	}
	control, exists := runtime.controls[input.PlayerID]
	if !exists {
		return ErrPlayerNotFound
	}
	if !runtime.simulation.player(input.PlayerID).state.IsAlive {
		return ErrPlayerEliminated
	}
	if !validID(input.ConnectionID) {
		return ErrInvalidInput
	}
	if input.RuntimeEpoch != runtime.epoch {
		return ErrStaleInput
	}
	if input.CreatedAt.IsZero() || now.Sub(input.CreatedAt) > InputLifetime || input.CreatedAt.Sub(now) > maximumCatchUp {
		return ErrInputExpired
	}
	if input.Kind == InputBind {
		select {
		case runtime.commands <- queuedInput{input, now}:
			return nil
		default:
			return ErrRuntimeBusy
		}
	}
	if control.connectionID != input.ConnectionID || control.generation == "" || control.generation != input.ControlGeneration {
		return ErrControlRequired
	}
	if !control.graceEndsAt.IsZero() || now.Sub(control.lastSeen) >= ControlTimeout {
		return ErrControlRequired
	}
	if input.Kind == InputSteer {
		if !(HouseRocketsSteerModel{ControlGeneration: input.ControlGeneration, InputSequence: input.InputSequence, Heading: input.Heading}).Valid() {
			return ErrInvalidInput
		}
		if input.InputSequence <= control.highestInput {
			return ErrStaleInput
		}
		if now.Sub(control.rateStartedAt) >= time.Second {
			control.rateStartedAt = now
			control.rateCount = 0
		}
		if control.rateCount >= MaximumSteerMessagesPerSecond {
			return ErrInputRate
		}
		control.rateCount++
		control.highestInput = input.InputSequence
		control.lastSeen = now
		runtime.pending[input.PlayerID] = queuedInput{input, now}
		return nil
	}
	if input.Kind == InputHeartbeat {
		control.lastSeen = now
		return nil
	}
	if input.Kind != InputDisconnect {
		return ErrInvalidInput
	}
	select {
	case runtime.commands <- queuedInput{input, now}:
		return nil
	default:
		return ErrRuntimeBusy
	}
}

func (runtime *Runtime) Run(ctx context.Context, leaseValid func() bool) {
	runtime.runOnce.Do(func() {
		defer func() { runtime.mutex.Lock(); runtime.stopped = true; runtime.mutex.Unlock() }()
		runtime.mutex.Lock()
		runtime.lastAdvance = time.Now()
		runtime.nextFrame = runtime.lastAdvance.Add(time.Second / SnapshotRateHz)
		for _, control := range runtime.controls {
			if control.lastSeen.IsZero() {
				control.graceEndsAt = runtime.lastAdvance.Add(ControlTimeout + ReconnectGraceDuration)
			}
		}
		runtime.mutex.Unlock()
		ticker := time.NewTicker(time.Second / SchedulingRateHz)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !leaseValid() {
					return
				}
				_ = runtime.Advance(time.Now())
			}
		}
	})
}

func (runtime *Runtime) Advance(now time.Time) error {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	if runtime.stopped {
		return ErrSimulationFinished
	}
	if now.Before(runtime.lastAdvance) {
		return ErrInvalidAdvance
	}
	if runtime.terminal {
		return nil
	}
	elapsed := now.Sub(runtime.lastAdvance)
	if elapsed > maximumCatchUp {
		_ = runtime.simulation.Cancel(EndRuntimeOverloaded)
		runtime.terminal = true
		runtime.publishFrame()
		return ErrInvalidAdvance
	}
	for range RuntimeQueueSize {
		select {
		case queued := <-runtime.commands:
			input := queued.input
			if now.Sub(queued.receivedAt) > InputLifetime {
				runtime.emit(RuntimeEvent{ConnectionID: input.ConnectionID, Err: ErrInputExpired})
				continue
			}
			control := runtime.controls[input.PlayerID]
			if !runtime.simulation.player(input.PlayerID).state.IsAlive {
				runtime.emit(RuntimeEvent{ConnectionID: input.ConnectionID, Err: ErrPlayerEliminated})
				continue
			}
			if input.Kind == InputBind {
				if (!control.graceEndsAt.IsZero() && !now.Before(control.graceEndsAt)) || (control.graceEndsAt.IsZero() && !now.Before(control.lastSeen.Add(ControlTimeout+ReconnectGraceDuration))) {
					runtime.emit(RuntimeEvent{ConnectionID: input.ConnectionID, Err: ErrControlRequired})
					continue
				}
				if control.connectionID != input.ConnectionID || control.generation == "" {
					control.connectionID, control.generation = input.ConnectionID, uuid.NewString()
					control.highestInput, control.processedInput = 0, 0
					delete(runtime.pending, input.PlayerID)
				}
				control.lastSeen, control.graceEndsAt = queued.receivedAt, time.Time{}
				grant := &HouseRocketsControlGrantedModel{SessionID: runtime.simulation.sessionID, PlayerID: input.PlayerID, RuntimeEpoch: runtime.epoch, ControlGeneration: control.generation}
				runtime.emit(RuntimeEvent{ConnectionID: input.ConnectionID, Grant: grant})
			} else if control.connectionID == input.ConnectionID && control.generation == input.ControlGeneration {
				control.connectionID, control.generation = "", ""
				control.graceEndsAt = queued.receivedAt.Add(ReconnectGraceDuration)
				delete(runtime.pending, input.PlayerID)
			}
		default:
			goto commandsDrained
		}
	}
commandsDrained:
	expired := make([]string, 0, MaximumPlayers)
	for id, control := range runtime.controls {
		if control.graceEndsAt.IsZero() && now.Sub(control.lastSeen) >= ControlTimeout {
			control.graceEndsAt = control.lastSeen.Add(ControlTimeout + ReconnectGraceDuration)
			control.connectionID, control.generation = "", ""
			delete(runtime.pending, id)
		}
		if !control.graceEndsAt.IsZero() && !now.Before(control.graceEndsAt) && runtime.simulation.player(id).state.IsAlive {
			expired = append(expired, id)
		}
	}
	if len(expired) != 0 {
		_ = runtime.simulation.EliminatePlayers(expired, EliminationConnectionExpired)
	}
	for id, queued := range runtime.pending {
		control := runtime.controls[id]
		if now.Sub(queued.receivedAt) <= InputLifetime && control.connectionID == queued.input.ConnectionID && control.generation == queued.input.ControlGeneration {
			if runtime.simulation.Steer(id, queued.input.Heading) == nil {
				control.processedInput = queued.input.InputSequence
			}
		}
		delete(runtime.pending, id)
	}
	runtime.remainder += int64(elapsed) * PhysicsRateHz
	ticks := runtime.remainder / int64(time.Second)
	runtime.remainder %= int64(time.Second)
	runtime.lastAdvance = now
	if runtime.simulation.outcome == nil && ticks > 0 {
		_ = runtime.simulation.AdvanceTicks(int(ticks))
	}
	if runtime.simulation.outcome != nil {
		runtime.terminal = true
	}
	if runtime.terminal || !now.Before(runtime.nextFrame) {
		interval := time.Second / SnapshotRateHz
		if !now.Before(runtime.nextFrame) {
			runtime.nextFrame = runtime.nextFrame.Add((now.Sub(runtime.nextFrame)/interval + 1) * interval)
		}
		runtime.publishFrame()
	}
	return nil
}

func (runtime *Runtime) Cancel(reason EndReason) error {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	if err := runtime.simulation.Cancel(reason); err != nil {
		return err
	}
	runtime.terminal = true
	runtime.publishFrame()
	return nil
}

func (runtime *Runtime) publishFrame() {
	runtime.sequence++
	frame := RuntimeFrame{RuntimeEpoch: runtime.epoch, StateSequence: runtime.sequence, Phase: PhasePlaying, World: runtime.simulation.Snapshot(), Controls: make([]RuntimePlayerControl, 0, len(runtime.controls))}
	if runtime.terminal {
		frame.Phase = PhaseFinalizing
	}
	for _, player := range frame.World.Players {
		control := runtime.controls[player.PlayerID]
		frame.Controls = append(frame.Controls, RuntimePlayerControl{PlayerID: player.PlayerID, Connected: control.connectionID != "", LastProcessedInputSequence: control.processedInput})
	}
	select {
	case <-runtime.frames:
	default:
	}
	runtime.frames <- frame
}

func (runtime *Runtime) emit(event RuntimeEvent) {
	select {
	case runtime.events <- event:
	default:
		_ = runtime.simulation.Cancel(EndRuntimeOverloaded)
		runtime.terminal = true
	}
}
