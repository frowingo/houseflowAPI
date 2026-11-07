package fausSwitch

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
)

// The runtime is the session layer of a race: it turns wall-clock time into
// ticks, owns control grants and heartbeats, queues each player's flips and
// hands them to the simulation once per scheduler step, and publishes frames,
// grants, rejections and the proposed result. It never performs database,
// Redis or socket I/O.

const (
	RuntimeQueueSize = 64
	// PlayerQueueSize bounds one player's flips waiting for the next step.
	PlayerQueueSize = 8
	InputLifetime   = time.Second
	// maximumCatchUp is the longest gap one Advance may cover; a longer one
	// means the scheduler starved and the race is cancelled as overloaded.
	// It is also what one simulation advance can step.
	maximumCatchUp = MaximumRewindDuration
)

// The longest catch-up must fit in one simulation advance.
var _ [maximumAdvanceTicks - int(maximumCatchUp*PhysicsRateHz/time.Second)]struct{}

const (
	InputBind       = module.InputBind
	InputFlip       = "flip"
	InputHeartbeat  = module.InputHeartbeat
	InputDisconnect = module.InputDisconnect
	InputSnapshot   = module.InputSnapshot
)

var (
	ErrStaleInput      = module.ErrStaleInput
	ErrInputExpired    = module.ErrInputExpired
	ErrRuntimeBusy     = module.ErrRuntimeBusy
	ErrInputRate       = module.ErrInputRate
	ErrControlRequired = module.ErrControlRequired
	ErrPlayerNotFound  = module.ErrPlayerNotFound
	ErrRuntimeFinished = module.ErrFinished
	ErrInvalidRace     = errInvalidRace
	// ErrRunnerFinished refuses a flip of a robot that had already finished
	// or crashed at the flip's tick, and every input of a player who left the
	// race. It carries its own code: the core's eliminated-player code is
	// not part of this game's contract.
	ErrRunnerFinished error = &helpers.ApplicationError{Kind: helpers.ErrorKindConflict, Key: RunnerFinishedErrorCode}
)

// RuntimeInput comes from the trusted gateway, which fills the identity;
// epoch and generation are opaque.
type RuntimeInput struct {
	MessageID         string    `json:"messageId,omitempty"`
	Kind              string    `json:"kind"`
	PlayerID          string    `json:"playerId"`
	ConnectionID      string    `json:"connectionId"`
	RuntimeEpoch      int64     `json:"runtimeEpoch"`
	ControlGeneration string    `json:"controlGeneration"`
	InputSequence     int64     `json:"inputSequence"`
	ClientTick        int64     `json:"clientTick"`
	CreatedAt         time.Time `json:"createdAt"`
}

type flipIntent struct{ FausSwitchFlipModel }

func (intent flipIntent) RuntimeInput(control module.ControlInput) any {
	return RuntimeInput{MessageID: control.MessageID, Kind: InputFlip, PlayerID: control.PlayerID, ConnectionID: control.ConnectionID, RuntimeEpoch: control.RuntimeEpoch,
		ControlGeneration: intent.ControlGeneration, InputSequence: intent.InputSequence, ClientTick: intent.ClientTick, CreatedAt: control.CreatedAt}
}

// RuntimePlayer is a frozen roster entry with the robot's final color.
type RuntimePlayer struct {
	PlayerID    string `json:"playerId"`
	DisplayName string `json:"displayName"`
	Color       Color  `json:"color"`
}

// RuntimeSession identifies the race a runtime plays; Players are in seat
// order.
type RuntimeSession struct {
	SessionID string          `json:"sessionId"`
	HouseID   string          `json:"houseId"`
	StartedAt time.Time       `json:"startedAt"`
	Players   []RuntimePlayer `json:"players"`
}

// runtimeSession freezes the roster with the colors the lobby chose.
func runtimeSession(sessionID, houseID string, startedAt time.Time, players []module.Player, lobbyState []byte) RuntimeSession {
	ids := make([]string, len(players))
	for seat, player := range players {
		ids[seat] = player.PlayerID
	}
	colors := assignColors(ids, lobbyState)
	session := RuntimeSession{SessionID: sessionID, HouseID: houseID, StartedAt: startedAt.UTC(), Players: make([]RuntimePlayer, len(players))}
	for seat, player := range players {
		session.Players[seat] = RuntimePlayer{PlayerID: player.PlayerID, DisplayName: player.DisplayName, Color: colors[seat]}
	}
	return session
}

func (session RuntimeSession) playerIDs() []string {
	ids := make([]string, len(session.Players))
	for seat, player := range session.Players {
		ids[seat] = player.PlayerID
	}
	return ids
}

type RuntimeEvent struct {
	MessageID    string
	Frame        *RuntimeFrame
	ConnectionID string
	Grant        *FausSwitchControlGrantedModel
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
	// queued counts this player's flips in the runtime's pending batch.
	queued int
	// left: the player left the race (forfeit or membership revoked); the
	// robot is out and the player never binds again.
	left bool
}

// pendingFlip is an accepted flip waiting for the next scheduler step.
type pendingFlip struct {
	seat       int
	sequence   int64
	clientTick int64
	messageID  string
	connection string
	createdAt  time.Time
	receivedAt time.Time
}

// Runtime isolates the race from all I/O. Run owns advancement; Submit only
// writes bounded mailboxes. Advance is exposed for deterministic scheduler
// tests. No callback runs under the mutex.
type Runtime struct {
	mutex           sync.Mutex
	settings        Settings
	session         RuntimeSession
	race            *simulation
	epoch           int64
	seats           map[string]int
	controls        []controller
	pending         []pendingFlip
	requests        []flipRequest
	commands        chan queuedInput
	frames          chan RuntimeFrame
	events          chan RuntimeEvent
	results         chan FausSwitchResultModel
	proposedResult  *FausSwitchResultModel
	committedResult *FausSwitchResultModel
	resultProposed  bool
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
	// criticalKeys are the step's critical keys before and after it, reused
	// so a step that publishes nothing allocates nothing.
	criticalKeys [2][]byte
}

func (settings Settings) controlTimeout() time.Duration {
	return time.Duration(settings.ControlTimeoutMilliseconds) * time.Millisecond
}

func (settings Settings) snapshotInterval() time.Duration {
	return time.Second / time.Duration(settings.SnapshotRateHz)
}

func ticksDuration(ticks int64) time.Duration {
	return time.Duration(ticks) * time.Second / PhysicsRateHz
}

func validID(value string) bool { return value != "" && len(value) <= 128 }

func newRuntime(settings Settings, session RuntimeSession, epoch int64, now time.Time, baseSequence int64) (*Runtime, error) {
	if !validRuntimeSession(session) || epoch <= 0 || now.IsZero() || baseSequence < 0 || settings.Validate() != nil {
		return nil, ErrInvalidRace
	}
	race, err := newSimulation(len(session.Players))
	if err != nil {
		return nil, err
	}
	runtime := newRuntimeShell(settings, session, race, epoch, now, baseSequence)
	runtime.publishFrame()
	return runtime, nil
}

// validRuntimeSession: an identified session with 2 to 8 distinct players,
// each with a distinct color.
func validRuntimeSession(session RuntimeSession) bool {
	count := len(session.Players)
	if !validID(session.SessionID) || !validID(session.HouseID) || session.StartedAt.IsZero() || count < MinimumPlayers || count > MaximumPlayers {
		return false
	}
	ids, colors := make(map[string]bool, count), make(map[Color]bool, count)
	for _, player := range session.Players {
		if !validID(player.PlayerID) || ids[player.PlayerID] || !player.Color.Valid() || colors[player.Color] {
			return false
		}
		ids[player.PlayerID], colors[player.Color] = true, true
	}
	return true
}

func newRuntimeShell(settings Settings, session RuntimeSession, race *simulation, epoch int64, now time.Time, baseSequence int64) *Runtime {
	runtime := &Runtime{settings: settings, session: session, race: race, epoch: epoch, seats: make(map[string]int, len(session.Players)),
		controls: make([]controller, len(session.Players)), pending: make([]pendingFlip, 0, len(session.Players)*PlayerQueueSize),
		requests: make([]flipRequest, 0, len(session.Players)*PlayerQueueSize), commands: make(chan queuedInput, RuntimeQueueSize),
		frames: make(chan RuntimeFrame, 1), events: make(chan RuntimeEvent, RuntimeQueueSize), results: make(chan FausSwitchResultModel, 1),
		lastAdvance: now, nextFrame: now.Add(settings.snapshotInterval()), sequence: baseSequence}
	for seat, player := range session.Players {
		runtime.seats[player.PlayerID] = seat
	}
	return runtime
}

func (runtime *Runtime) Frames() <-chan RuntimeFrame           { return runtime.frames }
func (runtime *Runtime) Events() <-chan RuntimeEvent           { return runtime.events }
func (runtime *Runtime) Results() <-chan FausSwitchResultModel { return runtime.results }

func (runtime *Runtime) SetObserver(observer module.RuntimeObserver) {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	runtime.observer = observer
}

// CommittedFrame is requested only after the immutable result commits.
func (runtime *Runtime) CommittedFrame(result FausSwitchResultModel) RuntimeFrame {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	runtime.committedResult = &result
	return runtime.buildFrame()
}

// Submit confirms mailbox acceptance only: the snapshot's
// lastProcessedInputSequence confirms a flip, and a refused one gets a
// targeted rejection when its step runs. Binds and disconnects queue for
// the next step; flips wait in the pending batch.
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
	seat, exists := runtime.seats[input.PlayerID]
	if !exists {
		return ErrPlayerNotFound
	}
	control := &runtime.controls[seat]
	if control.left {
		return ErrRunnerFinished
	}
	if input.Kind == InputBind {
		return runtime.enqueue(input, now)
	}
	if control.connectionID != input.ConnectionID || control.generation == "" || control.generation != input.ControlGeneration ||
		now.Sub(control.lastSeen) >= runtime.settings.controlTimeout() {
		return ErrControlRequired
	}
	switch input.Kind {
	case InputFlip:
		flip := FausSwitchFlipModel{ControlGeneration: input.ControlGeneration, InputSequence: input.InputSequence, ClientTick: input.ClientTick}
		if !flip.Valid() {
			return ErrInvalidInput
		}
		control.lastSeen = now
		// A resent flip is accepted again and never applied twice.
		if input.InputSequence <= control.highestInput {
			return nil
		}
		if now.Sub(control.rateStartedAt) >= time.Second {
			control.rateStartedAt, control.rateCount = now, 0
		}
		if control.rateCount >= runtime.settings.MaximumInputRateHz || control.queued >= PlayerQueueSize {
			return ErrInputRate
		}
		control.rateCount++
		control.queued++
		control.highestInput = input.InputSequence
		runtime.pending = append(runtime.pending, pendingFlip{seat: seat, sequence: input.InputSequence, clientTick: input.ClientTick,
			messageID: input.MessageID, connection: input.ConnectionID, createdAt: input.CreatedAt, receivedAt: now})
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

// Advance runs one scheduler step: binds and disconnects, expired controls,
// the race's ticks up to now, then the step's flips with one rewind, and a
// frame when something visible changed or a snapshot is due.
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
		runtime.end(EndRuntimeOverloaded)
		return ErrInvalidInput
	}
	runtime.criticalKeys[0] = runtime.appendCriticalKey(runtime.criticalKeys[0][:0])
	changed := runtime.dirty
	runtime.dirty = false
	changed = runtime.drainCommands(now) || changed
	for seat := range runtime.controls {
		if control := &runtime.controls[seat]; control.connectionID != "" && now.Sub(control.lastSeen) >= runtime.settings.controlTimeout() {
			runtime.dropControl(seat)
			changed = true
		}
	}
	runtime.remainder += int64(elapsed) * PhysicsRateHz
	ticks := runtime.remainder / int64(time.Second)
	runtime.remainder %= int64(time.Second)
	runtime.lastAdvance = now
	_ = runtime.race.advance(int(ticks))
	runtime.applyPending(now, &observation)
	if runtime.race.outcome != nil {
		runtime.terminal = true
	}
	runtime.criticalKeys[1] = runtime.appendCriticalKey(runtime.criticalKeys[1][:0])
	changed = changed || !bytes.Equal(runtime.criticalKeys[0], runtime.criticalKeys[1])
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
			seat := runtime.seats[input.PlayerID]
			control := &runtime.controls[seat]
			if control.left {
				runtime.emit(RuntimeEvent{ConnectionID: input.ConnectionID, MessageID: input.MessageID, Err: ErrRunnerFinished})
				continue
			}
			if input.Kind == InputBind {
				if control.connectionID != input.ConnectionID || control.generation == "" {
					runtime.dropControl(seat)
					control.connectionID, control.generation = input.ConnectionID, uuid.NewString()
					control.highestInput, control.processedInput = 0, 0
					changed = true
				}
				control.lastSeen = queued.receivedAt
				grant := &FausSwitchControlGrantedModel{SessionID: runtime.session.SessionID, PlayerID: input.PlayerID, RuntimeEpoch: runtime.epoch, ControlGeneration: control.generation}
				runtime.sequence++
				checkpoint := runtime.checkpointLocked(runtime.race.state())
				runtime.emit(RuntimeEvent{ConnectionID: input.ConnectionID, MessageID: input.MessageID, Grant: grant, Checkpoint: &checkpoint})
			} else if control.connectionID == input.ConnectionID && control.generation == input.ControlGeneration {
				runtime.dropControl(seat)
				changed = true
			}
		default:
			return changed
		}
	}
	return changed
}

// dropControl leaves the robot running without input until its player binds
// again; the player's waiting flips are discarded.
func (runtime *Runtime) dropControl(seat int) {
	control := &runtime.controls[seat]
	control.connectionID, control.generation = "", ""
	if control.queued == 0 {
		return
	}
	kept := runtime.pending[:0]
	for _, flip := range runtime.pending {
		if flip.seat != seat {
			kept = append(kept, flip)
		}
	}
	runtime.pending, control.queued = kept, 0
}

// applyPending hands the step's flips to the simulation as one batch, so a
// step costs at most one rewind. Every flip is processed: an applied or
// unsupported one is confirmed by lastProcessedInputSequence, a refused one
// (its robot had already stopped at the flip's tick) is rejected.
func (runtime *Runtime) applyPending(now time.Time, observation *module.RuntimeObservation) {
	if len(runtime.pending) == 0 {
		return
	}
	requests := runtime.requests[:0]
	for _, flip := range runtime.pending {
		requests = append(requests, flipRequest{seat: flip.seat, sequence: flip.sequence, clientTick: flip.clientTick})
	}
	err := runtime.race.applyFlips(requests)
	for index, flip := range runtime.pending {
		control := &runtime.controls[flip.seat]
		control.processedInput = flip.sequence
		if runtime.observer != nil {
			observation.InputAges = append(observation.InputAges, now.Sub(flip.createdAt))
		}
		switch {
		case err != nil:
			runtime.NotifyRejected(flip.connection, flip.messageID, ErrRuntimeFinished)
		case requests[index].outcome == flipNotRunning:
			runtime.NotifyRejected(flip.connection, flip.messageID, ErrRunnerFinished)
		}
	}
	if len(requests) > 1 {
		observation.InputsCoalesced += uint64(len(requests) - 1)
	}
	for seat := range runtime.controls {
		runtime.controls[seat].queued = 0
	}
	runtime.requests, runtime.pending = requests[:0], runtime.pending[:0]
}

// end finishes the race for a cancellation reason and publishes it.
func (runtime *Runtime) end(reason EndReason) {
	_ = runtime.race.cancel(reason)
	runtime.terminal = true
	runtime.publishFrame()
}

func (runtime *Runtime) Cancel(reason EndReason) error {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	if err := runtime.race.cancel(reason); err != nil {
		return err
	}
	runtime.terminal = true
	runtime.publishFrame()
	return nil
}

// Reconcile takes players who left the session (forfeit) or the house
// (membershipRevoked) out of the race. Their robots crash now; a rewind
// over this tick keeps the elimination.
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
	for seat, player := range runtime.session.Players {
		if runtime.controls[seat].left || (members[player.PlayerID] && playing[player.PlayerID]) {
			continue
		}
		reason := EliminationForfeit
		if !members[player.PlayerID] {
			reason = EliminationMembershipRevoked
		}
		if runtime.race.eliminate(seat, reason) != nil {
			return
		}
		runtime.dropControl(seat)
		runtime.controls[seat].left = true
		runtime.dirty = true
	}
}

func (runtime *Runtime) publishFrame() {
	if runtime.terminal && !runtime.resultProposed {
		if runtime.proposedResult == nil {
			if result, err := proposeResult(runtime.session, runtime.race, time.Now()); err == nil {
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

func (runtime *Runtime) buildFrame() RuntimeFrame {
	runtime.sequence++
	state := runtime.race.state()
	frame := RuntimeFrame{RuntimeEpoch: runtime.epoch, StateSequence: runtime.sequence, Phase: PhasePlaying,
		World: frameWorld(runtime.session, state, runtime.left()), Controls: make([]RuntimePlayerControl, len(runtime.session.Players))}
	if runtime.terminal {
		frame.Phase = PhaseFinalizing
	}
	if runtime.committedResult != nil {
		frame.CommittedResult, frame.Phase = runtime.committedResult, PhaseEnded
		if runtime.committedResult.Status == ResultCancelled {
			frame.Phase = PhaseCancelled
		}
	}
	for seat, player := range runtime.session.Players {
		control := &runtime.controls[seat]
		frame.Controls[seat] = RuntimePlayerControl{PlayerID: player.PlayerID, ConnectionID: control.connectionID, Connected: control.connectionID != "",
			LastProcessedInputSequence: control.processedInput}
	}
	checkpoint := runtime.checkpointLocked(state)
	frame.Checkpoint = &checkpoint
	return frame
}

func (runtime *Runtime) left() []bool {
	left := make([]bool, len(runtime.controls))
	for seat := range runtime.controls {
		left[seat] = runtime.controls[seat].left
	}
	return left
}

func (runtime *Runtime) emit(event RuntimeEvent) {
	select {
	case runtime.events <- event:
	default:
		runtime.end(EndRuntimeOverloaded)
	}
}

// NotifyRejected sends a bounded, targeted rejection; client misuse must not
// fill the mailbox and cancel an otherwise healthy race.
// It only writes the channel, so the runtime calls it under its lock too.
func (runtime *Runtime) NotifyRejected(connectionID, messageID string, err error) {
	select {
	case runtime.events <- RuntimeEvent{ConnectionID: connectionID, MessageID: messageID, Err: err}:
	default:
	}
}
