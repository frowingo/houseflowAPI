package houseTanks

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"houseflowApi/internal/application/game/module"
)

const (
	RuntimeQueueSize = 64
	// PlayerQueueSize bounds one player's presses waiting for their tick.
	PlayerQueueSize = 8
	maximumCatchUp  = 250 * time.Millisecond
)

var (
	ErrStaleInput       = module.ErrStaleInput
	ErrInputExpired     = module.ErrInputExpired
	ErrRuntimeBusy      = module.ErrRuntimeBusy
	ErrInputRate        = module.ErrInputRate
	ErrPlayerEliminated = module.ErrPlayerEliminated
	ErrPlayerNotFound   = module.ErrPlayerNotFound
	ErrRuntimeFinished  = module.ErrFinished
)

// RuntimePlayer is a frozen roster entry.
type RuntimePlayer struct {
	PlayerID    string `json:"playerId"`
	DisplayName string `json:"displayName"`
}

// RuntimeSession identifies the match a runtime plays.
type RuntimeSession struct {
	SessionID string          `json:"sessionId"`
	HouseID   string          `json:"houseId"`
	StartedAt time.Time       `json:"startedAt"`
	Players   []RuntimePlayer `json:"players"`
}

type RuntimePlayerControl struct {
	PlayerID                   string `json:"playerId"`
	ConnectionID               string `json:"connectionId"`
	Connected                  bool   `json:"connected"`
	LastProcessedInputSequence int64  `json:"lastProcessedInputSequence"`
}

// RuntimeFrame is the cross-instance frame: the transport reads its header
// fields, frameView renders the rest. World players' isAlive is the header's
// "has control rights in the match"; the tank's state is tankAlive.
type RuntimeFrame struct {
	RuntimeEpoch    int64                  `json:"runtimeEpoch"`
	StateSequence   int64                  `json:"stateSequence"`
	Phase           Phase                  `json:"phase"`
	World           FrameWorld             `json:"world"`
	Controls        []RuntimePlayerControl `json:"controls"`
	CommittedResult *HouseTanksResultModel `json:"committedResult,omitempty"`
	CountdownEndsAt *time.Time             `json:"countdownEndsAt,omitempty"`
	Checkpoint      *RuntimeCheckpoint     `json:"-"`
}

type RuntimeEvent struct {
	MessageID    string
	Frame        *RuntimeFrame
	ConnectionID string
	Grant        *HouseTanksControlGrantedModel
	Err          error
	Checkpoint   *RuntimeCheckpoint
}

type queuedInput struct {
	input      RuntimeInput
	receivedAt time.Time
}

type controller struct {
	connectionID   string
	generation     string
	lastSeen       time.Time
	highestInput   int64
	processedInput int64
	rateStartedAt  time.Time
	rateCount      int
	// queue keeps presses in order; one is applied per tick so a tap (press
	// and release within one scheduler step) is never swallowed.
	queue []queuedInput
}

// headingSample is the arena one tick after it was stepped, kept for lag
// compensation. It is not checkpointed: a recovered runtime uses its own
// headings until the window fills again.
type headingSample struct {
	tick      int64
	rotations []float64
	driving   []bool
}

// Runtime isolates the match from all database, Redis and socket I/O. Run
// owns advancement; Submit only writes bounded mailboxes. Advance is also
// exposed for deterministic scheduler tests. No callback runs under mutex.
type Runtime struct {
	mutex           sync.Mutex
	settings        Settings
	session         RuntimeSession
	match           *Match
	epoch           int64
	controls        map[string]*controller
	commands        chan queuedInput
	frames          chan RuntimeFrame
	events          chan RuntimeEvent
	results         chan HouseTanksResultModel
	history         []headingSample
	historyWorld    *World
	resultProposed  bool
	proposedResult  *HouseTanksResultModel
	committedResult *HouseTanksResultModel
	lastAdvance     time.Time
	nextFrame       time.Time
	remainder       int64
	sequence        int64
	dirty           bool
	terminal        bool
	stopped         bool
	runOnce         sync.Once
	observer        module.RuntimeObserver
	framesCoalesced uint64
}

func (settings Settings) reconnectGrace() time.Duration {
	return time.Duration(settings.ReconnectGraceMilliseconds) * time.Millisecond
}

func (settings Settings) snapshotInterval() time.Duration {
	return time.Second / time.Duration(settings.SnapshotRateHz)
}

// lagCompensationTicks is the window of past headings a press may claim.
func (settings Settings) lagCompensationTicks() int64 {
	return settings.LagCompensationMilliseconds * PhysicsRateHz / 1000
}

// matchTiming freezes the presence rules into a new match.
func (settings Settings) matchTiming() MatchTiming {
	return MatchTiming{InsufficientGraceTicks: max(1, durationTicks(settings.reconnectGrace())),
		PresenceGraceTicks: max(1, durationTicks(settings.controlTimeout()+settings.reconnectGrace()))}
}

// NewRuntime starts a match with the default settings.
func NewRuntime(session RuntimeSession, epoch int64, now time.Time) (*Runtime, error) {
	return newRuntime(DefaultSettings(), session, epoch, now, 0)
}

// NewRuntimeWithSettings runs a match with non-default settings, as a Game
// document would freeze them; load measurements compare snapshot rates with it.
func NewRuntimeWithSettings(settings Settings, session RuntimeSession, epoch int64, now time.Time) (*Runtime, error) {
	return newRuntime(settings, session, epoch, now, 0)
}

// WithMatch runs change on the match under the runtime's lock, for tests and
// tools that set up a situation; the next Advance publishes the result.
func (runtime *Runtime) WithMatch(change func(match *Match)) {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	change(runtime.match)
	runtime.dirty = true
}

func newRuntime(settings Settings, session RuntimeSession, epoch int64, now time.Time, baseSequence int64) (*Runtime, error) {
	if epoch <= 0 || now.IsZero() || baseSequence < 0 || settings.Validate() != nil || !validID(session.HouseID) || session.StartedAt.IsZero() {
		return nil, ErrInvalidMatch
	}
	ids := make([]string, len(session.Players))
	for index, player := range session.Players {
		ids[index] = player.PlayerID
	}
	match, err := NewMatch(session.SessionID, ids, settings.matchTiming())
	if err != nil {
		return nil, err
	}
	runtime := newRuntimeShell(settings, session, match, epoch, now, baseSequence)
	runtime.publishFrame()
	return runtime, nil
}

func newRuntimeShell(settings Settings, session RuntimeSession, match *Match, epoch int64, now time.Time, baseSequence int64) *Runtime {
	runtime := &Runtime{settings: settings, session: session, match: match, epoch: epoch, controls: make(map[string]*controller, len(session.Players)),
		commands: make(chan queuedInput, RuntimeQueueSize), frames: make(chan RuntimeFrame, 1), events: make(chan RuntimeEvent, RuntimeQueueSize),
		results: make(chan HouseTanksResultModel, 1), lastAdvance: now, nextFrame: now.Add(settings.snapshotInterval()), sequence: baseSequence}
	for _, player := range session.Players {
		runtime.controls[player.PlayerID] = &controller{}
	}
	runtime.recordHeadings()
	return runtime
}

func (runtime *Runtime) Frames() <-chan RuntimeFrame           { return runtime.frames }
func (runtime *Runtime) Events() <-chan RuntimeEvent           { return runtime.events }
func (runtime *Runtime) Results() <-chan HouseTanksResultModel { return runtime.results }

func (runtime *Runtime) SetObserver(observer module.RuntimeObserver) {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	runtime.observer = observer
}

// CommittedFrame is requested only after the immutable result commits.
func (runtime *Runtime) CommittedFrame(result HouseTanksResultModel) RuntimeFrame {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	runtime.committedResult = &result
	return runtime.buildFrame()
}

// Submit confirms mailbox acceptance only; grants and snapshot ACKs confirm
// processing. Presses queue per player; votes apply at once.
func (runtime *Runtime) Submit(input RuntimeInput, now time.Time) error {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	if runtime.stopped {
		return ErrRuntimeFinished
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
	if input.Kind == InputSnapshot {
		frame := runtime.buildFrame()
		runtime.emit(RuntimeEvent{ConnectionID: input.ConnectionID, MessageID: input.MessageID, Frame: &frame})
		return nil
	}
	if runtime.terminal {
		return ErrRuntimeFinished
	}
	control, exists := runtime.controls[input.PlayerID]
	if !exists {
		return ErrPlayerNotFound
	}
	if player, _ := runtime.match.Player(input.PlayerID); player.HasLeft {
		return ErrPlayerEliminated
	}
	if input.Kind == InputBind {
		return runtime.enqueue(input, now)
	}
	if control.connectionID != input.ConnectionID || control.generation == "" || control.generation != input.ControlGeneration ||
		now.Sub(control.lastSeen) >= runtime.settings.controlTimeout() {
		return ErrControlRequired
	}
	switch input.Kind {
	case InputPress:
		press := HouseTanksPressModel{ControlGeneration: input.ControlGeneration, InputSequence: input.InputSequence, Pressed: input.Pressed, ClientTick: input.ClientTick, Heading: input.Heading}
		if !press.Valid() {
			return ErrInvalidInput
		}
		if input.InputSequence <= control.highestInput {
			return ErrStaleInput
		}
		if runtime.match.Playing() {
			if tank, _ := runtime.match.World.TankOf(input.PlayerID); !tank.Active {
				return ErrPlayerEliminated
			}
		}
		if now.Sub(control.rateStartedAt) >= time.Second {
			control.rateStartedAt, control.rateCount = now, 0
		}
		if control.rateCount >= runtime.settings.MaximumPressMessagesPerSecond || len(control.queue) >= PlayerQueueSize {
			return ErrInputRate
		}
		control.rateCount++
		control.highestInput, control.lastSeen = input.InputSequence, now
		control.queue = append(control.queue, queuedInput{input, now})
		return nil
	case InputRoundReady:
		if !(HouseTanksRoundReadyModel{ControlGeneration: input.ControlGeneration, Ready: input.Ready}).Valid() {
			return ErrInvalidInput
		}
		if err := runtime.match.SetReady(input.PlayerID, input.Ready); err != nil {
			return err
		}
		control.lastSeen, runtime.dirty = now, true
		return nil
	case InputHeartbeat:
		control.lastSeen = now
		return nil
	case InputDisconnect:
		return runtime.enqueue(input, now)
	}
	return ErrInvalidInput
}

func (runtime *Runtime) enqueue(input RuntimeInput, now time.Time) error {
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
		runtime.nextFrame = runtime.lastAdvance.Add(runtime.settings.snapshotInterval())
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
	started := time.Now()
	runtime.mutex.Lock()
	observer, framesBefore := runtime.observer, runtime.framesCoalesced
	observation := module.RuntimeObservation{StepGap: now.Sub(runtime.lastAdvance)}
	measureStep := !runtime.terminal && !runtime.stopped && !now.Before(runtime.lastAdvance)
	defer func() {
		observation.FramesCoalesced = runtime.framesCoalesced - framesBefore
		if measureStep {
			observation.StepDuration = time.Since(started)
		}
		runtime.mutex.Unlock()
		if observer != nil {
			observer.ObserveRuntime(observation)
		}
	}()
	if runtime.stopped {
		return ErrRuntimeFinished
	}
	if now.Before(runtime.lastAdvance) {
		return ErrInvalidInput
	}
	if runtime.terminal {
		if !runtime.resultProposed {
			runtime.publishFrame()
		}
		return nil
	}
	elapsed := now.Sub(runtime.lastAdvance)
	if elapsed > maximumCatchUp {
		observation.Overloaded = true
		_ = runtime.match.Cancel(EndRuntimeOverloaded)
		runtime.terminal = true
		runtime.publishFrame()
		return ErrInvalidInput
	}
	criticalBefore := runtime.criticalKey()
	changed := runtime.dirty
	runtime.dirty = false
	changed = runtime.drainCommands(now) || changed
	for id, control := range runtime.controls {
		if control.connectionID != "" && now.Sub(control.lastSeen) >= runtime.settings.controlTimeout() {
			runtime.dropControl(id)
			changed = true
		}
	}
	runtime.remainder += int64(elapsed) * PhysicsRateHz
	ticks := runtime.remainder / int64(time.Second)
	runtime.remainder %= int64(time.Second)
	runtime.lastAdvance = now
	for range ticks {
		if runtime.match.Ended() {
			break
		}
		runtime.applyInputs(now, &observation)
		runtime.match.Advance()
		runtime.recordHeadings()
	}
	if runtime.match.Ended() {
		runtime.terminal = true
	}
	changed = changed || runtime.criticalKey() != criticalBefore
	if changed || runtime.terminal || !now.Before(runtime.nextFrame) {
		interval := runtime.settings.snapshotInterval()
		if !now.Before(runtime.nextFrame) {
			runtime.nextFrame = runtime.nextFrame.Add((now.Sub(runtime.nextFrame)/interval + 1) * interval)
		}
		runtime.publishFrame()
	}
	return nil
}

// drainCommands applies binds and disconnects in arrival order.
func (runtime *Runtime) drainCommands(now time.Time) bool {
	changed := false
	for range RuntimeQueueSize {
		select {
		case queued := <-runtime.commands:
			input := queued.input
			if now.Sub(queued.receivedAt) > InputLifetime {
				runtime.emit(RuntimeEvent{ConnectionID: input.ConnectionID, MessageID: input.MessageID, Err: ErrInputExpired})
				continue
			}
			control := runtime.controls[input.PlayerID]
			if player, _ := runtime.match.Player(input.PlayerID); player.HasLeft {
				runtime.emit(RuntimeEvent{ConnectionID: input.ConnectionID, MessageID: input.MessageID, Err: ErrPlayerEliminated})
				continue
			}
			if input.Kind == InputBind {
				if control.connectionID != input.ConnectionID || control.generation == "" {
					changed = true
					control.connectionID, control.generation = input.ConnectionID, uuid.NewString()
					control.highestInput, control.processedInput, control.queue = 0, 0, nil
					_ = runtime.match.SetControl(input.PlayerID, true)
				}
				control.lastSeen = queued.receivedAt
				grant := &HouseTanksControlGrantedModel{SessionID: runtime.session.SessionID, PlayerID: input.PlayerID, RuntimeEpoch: runtime.epoch, ControlGeneration: control.generation}
				runtime.sequence++
				checkpoint := runtime.checkpointLocked()
				runtime.emit(RuntimeEvent{ConnectionID: input.ConnectionID, MessageID: input.MessageID, Grant: grant, Checkpoint: &checkpoint})
			} else if control.connectionID == input.ConnectionID && control.generation == input.ControlGeneration {
				runtime.dropControl(input.PlayerID)
				changed = true
			}
		default:
			return changed
		}
	}
	return changed
}

// dropControl turns the player's tank into a stationary target until a new
// binding; its queued presses are discarded.
func (runtime *Runtime) dropControl(playerID string) {
	control := runtime.controls[playerID]
	control.connectionID, control.generation, control.queue = "", "", nil
	_ = runtime.match.SetControl(playerID, false)
}

// applyInputs takes the oldest queued press of every player for this tick.
// Presses outside roundPlaying are acknowledged and ignored, so races at a
// round boundary do not produce errors.
func (runtime *Runtime) applyInputs(now time.Time, observation *module.RuntimeObservation) {
	for _, player := range runtime.session.Players {
		control := runtime.controls[player.PlayerID]
		for len(control.queue) > 0 {
			queued := control.queue[0]
			control.queue = control.queue[1:]
			if now.Sub(queued.receivedAt) > InputLifetime {
				continue
			}
			control.processedInput = queued.input.InputSequence
			if runtime.observer != nil {
				observation.InputAges = append(observation.InputAges, now.Sub(queued.input.CreatedAt))
			}
			runtime.applyPress(player.PlayerID, queued.input)
			break
		}
		if len(control.queue) == 0 {
			control.queue = nil
		}
	}
}

func (runtime *Runtime) applyPress(playerID string, input RuntimeInput) {
	if !runtime.match.Playing() {
		return
	}
	world := runtime.match.World
	if !input.Pressed {
		world.Release(playerID)
		return
	}
	tank, found := world.TankOf(playerID)
	if !found || !tank.Active || !tank.Controllable || tank.Driving {
		return
	}
	if heading, compensated := runtime.compensatedHeading(tank, input.ClientTick, input.Heading); compensated {
		world.SetRotation(playerID, heading)
	}
	world.Press(playerID)
}

// compensatedHeading accepts the heading the client saw when it lies on the
// arc the tank swept since the client's tick, widened by the tolerance. A
// tank that drove or held an aim lock in that window keeps its own heading.
func (runtime *Runtime) compensatedHeading(tank Tank, clientTick int64, heading float64) (float64, bool) {
	serverTick, window := runtime.match.Tick, runtime.settings.lagCompensationTicks()
	if window == 0 || tank.AimLocks > 0 || clientTick > serverTick+2 || serverTick-clientTick > window {
		return 0, false
	}
	from := min(clientTick, serverTick)
	low, high, samples := math.Inf(1), math.Inf(-1), int64(0)
	for _, sample := range runtime.history {
		if sample.tick < from || sample.tick > serverTick {
			continue
		}
		if sample.driving[tank.Seat] {
			return 0, false
		}
		low, high, samples = math.Min(low, sample.rotations[tank.Seat]), math.Max(high, sample.rotations[tank.Seat]), samples+1
	}
	if samples != serverTick-from+1 {
		return 0, false
	}
	candidate := tank.Rotation + NormalizedAngle(heading-tank.Rotation)
	tolerance := float64(runtime.settings.HeadingToleranceMilliradians) / 1000
	if candidate < low-tolerance || candidate > high+tolerance {
		return 0, false
	}
	return candidate, true
}

// recordHeadings keeps the last window of tank headings of the current arena.
func (runtime *Runtime) recordHeadings() {
	world := runtime.match.World
	if world != runtime.historyWorld {
		runtime.history, runtime.historyWorld = runtime.history[:0], world
	}
	sample := headingSample{tick: runtime.match.Tick, rotations: make([]float64, len(world.Tanks)), driving: make([]bool, len(world.Tanks))}
	for index, tank := range world.Tanks {
		sample.rotations[index], sample.driving[index] = tank.Rotation, tank.Driving
	}
	limit := int(runtime.settings.lagCompensationTicks()) + 3
	if len(runtime.history) >= limit {
		runtime.history = append(runtime.history[:0], runtime.history[len(runtime.history)-limit+1:]...)
	}
	runtime.history = append(runtime.history, sample)
}

func (runtime *Runtime) Cancel(reason EndReason) error {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	if err := runtime.match.Cancel(reason); err != nil {
		return err
	}
	runtime.terminal = true
	runtime.publishFrame()
	return nil
}

// Reconcile makes players who left the session or lost membership permanent
// stationary targets; they keep their seat and scores but never bind again.
func (runtime *Runtime) Reconcile(playingIDs, memberIDs []string) {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	if runtime.terminal {
		return
	}
	playing, members := make(map[string]bool, len(playingIDs)), make(map[string]bool, len(memberIDs))
	for _, id := range playingIDs {
		playing[id] = true
	}
	for _, id := range memberIDs {
		members[id] = true
	}
	for _, player := range runtime.session.Players {
		if state, _ := runtime.match.Player(player.PlayerID); !state.HasLeft && (!members[player.PlayerID] || !playing[player.PlayerID]) {
			runtime.dropControl(player.PlayerID)
			_ = runtime.match.Leave(player.PlayerID)
			runtime.dirty = true
		}
	}
}

func (runtime *Runtime) publishFrame() {
	if runtime.terminal && !runtime.resultProposed {
		endedAt := time.Now().UTC()
		if minimumEnd := runtime.session.StartedAt.Add(ticksDuration(runtime.match.Tick)); endedAt.Before(minimumEnd) {
			endedAt = minimumEnd
		}
		if runtime.proposedResult == nil {
			if result, err := proposeResult(runtime.session, runtime.match, endedAt); err == nil {
				runtime.proposedResult = &result
			}
		}
		if runtime.proposedResult != nil {
			runtime.results <- *runtime.proposedResult
			runtime.resultProposed = true
		}
	}
	frame := runtime.buildFrame()
	select {
	case <-runtime.frames:
		runtime.framesCoalesced++
	default:
	}
	runtime.frames <- frame
}

func ticksDuration(ticks int64) time.Duration {
	return time.Duration(ticks) * time.Second / PhysicsRateHz
}

func (runtime *Runtime) buildFrame() RuntimeFrame {
	runtime.sequence++
	frame := RuntimeFrame{RuntimeEpoch: runtime.epoch, StateSequence: runtime.sequence, Phase: PhasePlaying, World: frameWorld(runtime.session, runtime.match),
		Controls: make([]RuntimePlayerControl, 0, len(runtime.session.Players))}
	if runtime.terminal {
		frame.Phase = PhaseFinalizing
	}
	if runtime.committedResult != nil {
		frame.CommittedResult = runtime.committedResult
		frame.Phase = PhaseEnded
		if runtime.committedResult.Status == ResultCancelled {
			frame.Phase = PhaseCancelled
		}
	}
	for _, player := range runtime.session.Players {
		control := runtime.controls[player.PlayerID]
		frame.Controls = append(frame.Controls, RuntimePlayerControl{PlayerID: player.PlayerID, ConnectionID: control.connectionID, Connected: control.connectionID != "",
			LastProcessedInputSequence: control.processedInput})
	}
	checkpoint := runtime.checkpointLocked()
	frame.Checkpoint = &checkpoint
	return frame
}

func (runtime *Runtime) emit(event RuntimeEvent) {
	select {
	case runtime.events <- event:
	default:
		_ = runtime.match.Cancel(EndRuntimeOverloaded)
		runtime.terminal = true
		runtime.publishFrame()
	}
}

// NotifyRejected sends a bounded, targeted rejection; client misuse must not
// fill the mailbox and cancel an otherwise healthy match.
func (runtime *Runtime) NotifyRejected(connectionID, messageID string, err error) {
	select {
	case runtime.events <- RuntimeEvent{ConnectionID: connectionID, MessageID: messageID, Err: err}:
	default:
	}
}
