package fausSwitch

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
)

// oneTick advances exactly one physics tick per Advance.
const oneTick = time.Second/PhysicsRateHz + 1

// raceDriver plays a runtime tick by tick: it keeps bound players alive with
// heartbeats and drains every publication so the mailboxes never fill.
type raceDriver struct {
	t           testing.TB
	runtime     *Runtime
	epoch       int64
	now         time.Time
	grants      map[string]FausSwitchControlGrantedModel
	connections map[string]string
	sequences   map[string]int64
	silent      map[string]bool
	events      []RuntimeEvent
	frame       RuntimeFrame
	results     []FausSwitchResultModel
}

func playerID(seat int) string { return fmt.Sprintf("player-%d", seat+1) }

func raceSession(count int, startedAt time.Time) RuntimeSession {
	players := make([]module.Player, count)
	for seat := range players {
		players[seat] = module.Player{PlayerID: playerID(seat), DisplayName: fmt.Sprintf("Robot %d", seat+1)}
	}
	return runtimeSession("race-session", "race-house", startedAt, players, nil)
}

func newDriver(t testing.TB, count int) *raceDriver {
	t.Helper()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	runtime, err := newRuntime(DefaultSettings(), raceSession(count, now), 7, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	return attachDriver(t, runtime, 7, now)
}

func attachDriver(t testing.TB, runtime *Runtime, epoch int64, now time.Time) *raceDriver {
	driver := &raceDriver{t: t, runtime: runtime, epoch: epoch, now: now, grants: map[string]FausSwitchControlGrantedModel{},
		connections: map[string]string{}, sequences: map[string]int64{}, silent: map[string]bool{}}
	driver.drain()
	return driver
}

func (driver *raceDriver) drain() {
	for {
		select {
		case frame := <-driver.runtime.Frames():
			driver.frame = frame
		case event := <-driver.runtime.Events():
			driver.events = append(driver.events, event)
		case result := <-driver.runtime.Results():
			driver.results = append(driver.results, result)
		default:
			return
		}
	}
}

func (driver *raceDriver) input(id, kind string) RuntimeInput {
	return RuntimeInput{Kind: kind, PlayerID: id, ConnectionID: driver.connections[id], RuntimeEpoch: driver.epoch,
		ControlGeneration: driver.grants[id].ControlGeneration, CreatedAt: driver.now}
}

func (driver *raceDriver) bind(id string) FausSwitchControlGrantedModel {
	driver.t.Helper()
	connection := "connection-" + id
	input := RuntimeInput{Kind: InputBind, PlayerID: id, ConnectionID: connection, RuntimeEpoch: driver.epoch, CreatedAt: driver.now}
	if err := driver.runtime.Submit(input, driver.now); err != nil {
		driver.t.Fatalf("bind %s: %v", id, err)
	}
	driver.advance()
	for index := len(driver.events) - 1; index >= 0; index-- {
		if event := driver.events[index]; event.Grant != nil && event.Grant.PlayerID == id && event.ConnectionID == connection {
			driver.grants[id], driver.connections[id], driver.sequences[id] = *event.Grant, connection, 0
			return *event.Grant
		}
	}
	driver.t.Fatalf("no grant for %s", id)
	return FausSwitchControlGrantedModel{}
}

func (driver *raceDriver) bindAll() {
	for seat := range driver.runtime.session.Players {
		driver.bind(playerID(seat))
	}
}

func (driver *raceDriver) advance() {
	driver.t.Helper()
	for id := range driver.grants {
		if !driver.silent[id] {
			_ = driver.runtime.Submit(driver.input(id, InputHeartbeat), driver.now)
		}
	}
	driver.now = driver.now.Add(oneTick)
	if err := driver.runtime.Advance(driver.now); err != nil && !errors.Is(err, ErrRuntimeFinished) {
		driver.t.Fatal(err)
	}
	driver.drain()
}

func (driver *raceDriver) ticks(count int) {
	for range count {
		driver.advance()
	}
}

func (driver *raceDriver) tick() int64 {
	driver.runtime.mutex.Lock()
	defer driver.runtime.mutex.Unlock()
	return driver.runtime.race.tick
}

// processed is the seat's lastProcessedInputSequence now; frames are only
// published at the snapshot rate.
func (driver *raceDriver) processed(seat int) int64 {
	driver.runtime.mutex.Lock()
	defer driver.runtime.mutex.Unlock()
	return driver.runtime.controls[seat].processedInput
}

// untilSupported plays until the robot stands on a surface.
func (driver *raceDriver) untilSupported(seat int) {
	driver.t.Helper()
	for range PhysicsRateHz * 2 {
		driver.runtime.mutex.Lock()
		race := driver.runtime.race
		supported := race.rules.supported(race.runners(), seat)
		driver.runtime.mutex.Unlock()
		if supported {
			return
		}
		driver.advance()
	}
	driver.t.Fatal("the robot never landed")
}

func (driver *raceDriver) world() world {
	driver.runtime.mutex.Lock()
	defer driver.runtime.mutex.Unlock()
	return driver.runtime.race.world
}

// flip sends the player's next flip for clientTick.
func (driver *raceDriver) flip(id string, clientTick int64) (string, error) {
	driver.sequences[id]++
	input := driver.input(id, InputFlip)
	input.InputSequence, input.ClientTick = driver.sequences[id], clientTick
	input.MessageID = fmt.Sprintf("%s-flip-%d", id, input.InputSequence)
	return input.MessageID, driver.runtime.Submit(input, driver.now)
}

func (driver *raceDriver) rejection(messageID string) error {
	for _, event := range driver.events {
		if event.MessageID == messageID && event.Err != nil {
			return event.Err
		}
	}
	return nil
}

// untilResult plays until the runtime proposes its result.
func (driver *raceDriver) untilResult() FausSwitchResultModel {
	driver.t.Helper()
	for range MaximumMatchTicks + 10 {
		if len(driver.results) > 0 {
			return driver.results[0]
		}
		driver.advance()
	}
	driver.t.Fatal("the race never ended")
	return FausSwitchResultModel{}
}

// raceSnapshot is the stored session the runtime's results belong to.
func raceSnapshot(session RuntimeSession) gameDomain.SessionSnapshot {
	snapshot := gameDomain.SessionSnapshot{SessionID: session.SessionID, HouseID: session.HouseID, GameKey: GameKey, StartedAt: session.StartedAt,
		State: gameDomain.SessionRunning, Rules: Definition().Rules}
	for _, player := range session.Players {
		snapshot.Players = append(snapshot.Players, gameDomain.SessionPlayer{PlayerID: player.PlayerID, State: gameDomain.PlayerPlaying,
			JoinedAt: session.StartedAt.Add(-time.Minute), ReadyAt: session.StartedAt.Add(-time.Minute)})
	}
	return snapshot
}

// Briefing §8: four robots race at full size, five or more at 0.75; the
// scale is fixed by the roster frozen at the countdown, so a player who left
// during it keeps the seat and forfeits at tick 0.
func TestRuntimeRunnerScale(t *testing.T) {
	for count, scale := range map[int]float64{2: 1, 4: 1, 5: CrowdedRunnerScale, 8: CrowdedRunnerScale} {
		driver := newDriver(t, count)
		if driver.frame.World.RunnerScale != scale || len(driver.frame.World.Players) != count || driver.frame.World.Tick != 0 {
			t.Fatalf("%d robots: scale %v", count, driver.frame.World.RunnerScale)
		}
	}
	driver := newDriver(t, 5)
	ids := []string{playerID(0), playerID(1), playerID(2), playerID(3)}
	driver.runtime.Reconcile(ids, append(ids, playerID(4)))
	driver.advance()
	leaver := driver.frame.World.Players[4]
	if driver.frame.World.RunnerScale != CrowdedRunnerScale || leaver.IsAlive || leaver.Status != RunnerCrashed ||
		*leaver.EliminationReason != EliminationForfeit || *leaver.EliminatedAtTick != 0 {
		t.Fatalf("countdown leaver: %+v", leaver)
	}
}

func TestRuntimeControl(t *testing.T) {
	driver := newDriver(t, 2)
	a, b := playerID(0), playerID(1)
	driver.connections[a] = "connection-" + a
	if _, err := driver.flip(a, 0); !errors.Is(err, ErrControlRequired) {
		t.Fatalf("flip without control: %v", err)
	}
	grant := driver.bind(a)
	if grant.SessionID != "race-session" || grant.RuntimeEpoch != 7 || grant.ControlGeneration == "" || !driver.frame.Controls[0].Connected || driver.frame.Controls[1].Connected {
		t.Fatalf("grant %+v", grant)
	}
	stale := driver.input(a, InputFlip)
	stale.InputSequence, stale.ControlGeneration = 1, "old"
	if err := driver.runtime.Submit(stale, driver.now); !errors.Is(err, ErrControlRequired) {
		t.Fatalf("old generation: %v", err)
	}
	stale = driver.input(a, InputFlip)
	stale.InputSequence, stale.RuntimeEpoch = 1, 6
	if err := driver.runtime.Submit(stale, driver.now); !errors.Is(err, ErrStaleInput) {
		t.Fatalf("old epoch: %v", err)
	}
	stale = driver.input(a, InputFlip)
	stale.InputSequence, stale.CreatedAt = 1, driver.now.Add(-2*InputLifetime)
	if err := driver.runtime.Submit(stale, driver.now); !errors.Is(err, ErrInputExpired) {
		t.Fatalf("expired: %v", err)
	}
	unknown := driver.input(a, InputFlip)
	unknown.PlayerID = "stranger"
	if err := driver.runtime.Submit(unknown, driver.now); !errors.Is(err, ErrPlayerNotFound) {
		t.Fatalf("stranger: %v", err)
	}
	// A rebind on another connection replaces the generation.
	driver.bind(b)
	again := driver.input(b, InputBind)
	again.ConnectionID = "second-connection"
	_ = driver.runtime.Submit(again, driver.now)
	driver.advance()
	if driver.frame.Controls[1].ConnectionID != "second-connection" {
		t.Fatal("rebind")
	}
	if _, err := driver.flip(b, driver.tick()); !errors.Is(err, ErrControlRequired) {
		t.Fatalf("flip from the replaced connection: %v", err)
	}
	// Without heartbeats control expires; the robot runs on without input
	// (and here meets its first wall alone), and the player may bind again.
	driver.silent[a] = true
	driver.ticks(int(ControlTimeout/oneTick) + 2)
	if robot := driver.frame.World.Players[0]; driver.frame.Controls[0].Connected || !robot.IsAlive ||
		(robot.EliminationReason != nil && *robot.EliminationReason != EliminationLeftBehind && *robot.EliminationReason != EliminationBoundary) {
		t.Fatalf("expired control: %+v %+v", driver.frame.Controls[0], robot.runnerState)
	}
	// A disconnect drops it at once.
	driver.silent[a] = false
	driver.bind(a)
	if err := driver.runtime.Submit(driver.input(a, InputDisconnect), driver.now); err != nil {
		t.Fatal(err)
	}
	driver.advance()
	if driver.frame.Controls[0].Connected {
		t.Fatal("disconnect")
	}
	// A snapshot request is answered to that connection alone.
	request := driver.input(b, InputSnapshot)
	request.MessageID = "resync"
	if err := driver.runtime.Submit(request, driver.now); err != nil {
		t.Fatal(err)
	}
	driver.drain()
	last := driver.events[len(driver.events)-1]
	if last.MessageID != "resync" || last.Frame == nil || last.Frame.StateSequence <= driver.frame.StateSequence-1 {
		t.Fatalf("resync answer %+v", last)
	}
}

func TestRuntimeFlips(t *testing.T) {
	driver := newDriver(t, 2)
	a := playerID(0)
	driver.bindAll()
	// The robots drop from their start platforms first.
	driver.untilSupported(0)
	before := driver.world()[0].gravity
	if _, err := driver.flip(a, driver.tick()); err != nil {
		t.Fatal(err)
	}
	driver.advance()
	if driver.world()[0].gravity == before || driver.processed(0) != 1 {
		t.Fatal("the flip was not applied")
	}
	// A resent flip is accepted and not applied twice, even once the robot
	// stands on the other rail and could flip again.
	driver.untilSupported(0)
	resent := driver.input(a, InputFlip)
	resent.InputSequence, resent.ClientTick = 1, driver.tick()
	if err := driver.runtime.Submit(resent, driver.now); err != nil {
		t.Fatalf("resent flip: %v", err)
	}
	driver.ticks(2)
	if driver.world()[0].gravity == before || driver.processed(0) != 1 {
		t.Fatal("the resent flip was applied again")
	}
	// At most MaximumInputRateHz flips a second.
	rateLimited := false
	for range MaximumInputRateHz + 1 {
		if _, err := driver.flip(a, driver.tick()); errors.Is(err, ErrInputRate) {
			rateLimited = true
		}
		driver.advance()
	}
	if !rateLimited {
		t.Fatal("no rate limit")
	}
}

// A flip 20 ticks late lands where it would have on time: the runtime runs
// the same race as one that received it promptly.
func TestRuntimeLateFlipMatchesOnTime(t *testing.T) {
	onTime, late := newDriver(t, 4), newDriver(t, 4)
	onTime.bindAll()
	late.bindAll()
	const flipAt = 90
	onTime.ticks(flipAt - int(onTime.tick()))
	late.ticks(flipAt - int(late.tick()))
	if _, err := onTime.flip(playerID(1), flipAt); err != nil {
		t.Fatal(err)
	}
	late.ticks(20)
	if _, err := late.flip(playerID(1), flipAt); err != nil {
		t.Fatal(err)
	}
	onTime.ticks(int(late.tick()-onTime.tick()) + 40)
	late.ticks(40)
	if onTime.tick() != late.tick() || onTime.world() != late.world() {
		t.Fatal("the late flip changed the race")
	}
	unflipped := newDriver(t, 4)
	unflipped.ticks(int(late.tick()))
	if unflipped.world() == late.world() {
		t.Fatal("the flip did nothing")
	}
}

// A flip of a robot that had already stopped at the flip's tick is refused
// with runner_finished; once the race is over every flip is not_running.
func TestRuntimeRefusesStoppedRobots(t *testing.T) {
	driver := newDriver(t, 2)
	driver.bindAll()
	driver.runtime.mutex.Lock()
	_ = driver.runtime.race.eliminate(0, EliminationForfeit)
	driver.runtime.mutex.Unlock()
	driver.ticks(MaximumRewindTicks + 5)
	messageID, err := driver.flip(playerID(0), 0)
	if err != nil {
		t.Fatalf("a crashed robot's flip is refused when it applies: %v", err)
	}
	driver.advance()
	refusal := driver.rejection(messageID)
	var applicationError *helpers.ApplicationError
	if !errors.As(refusal, &applicationError) || applicationError.Key != RunnerFinishedErrorCode || errors.Is(refusal, module.ErrPlayerEliminated) {
		t.Fatalf("refusal: %v", refusal)
	}
	if driver.processed(0) != 1 || driver.world()[0].status != RunnerCrashed {
		t.Fatal("a refused flip is processed and changes nothing")
	}
	driver.runtime.Cancel(EndCancelledByUser)
	driver.drain()
	if _, err := driver.flip(playerID(1), driver.tick()); !errors.Is(err, ErrRuntimeFinished) {
		t.Fatalf("after the race: %v", err)
	}
}

// Leaving the session forfeits, losing the house membership revokes; the
// player is out of the race for good.
func TestRuntimeLeavers(t *testing.T) {
	driver := newDriver(t, 3)
	driver.bindAll()
	a, b, c := playerID(0), playerID(1), playerID(2)
	driver.ticks(10)
	driver.runtime.Reconcile([]string{b}, []string{a, b})
	driver.advance()
	players := driver.frame.World.Players
	if players[0].IsAlive || *players[0].EliminationReason != EliminationForfeit || players[2].IsAlive ||
		*players[2].EliminationReason != EliminationMembershipRevoked || !players[1].IsAlive || driver.frame.Controls[0].Connected {
		t.Fatalf("leavers: %+v", players)
	}
	if _, err := driver.flip(a, driver.tick()); !errors.Is(err, ErrRunnerFinished) {
		t.Fatalf("a leaver's flip: %v", err)
	}
	if err := driver.runtime.Submit(RuntimeInput{Kind: InputBind, PlayerID: c, ConnectionID: "back", RuntimeEpoch: 7, CreatedAt: driver.now}, driver.now); !errors.Is(err, ErrRunnerFinished) {
		t.Fatalf("a leaver binds again: %v", err)
	}
	// Once the last runner leaves, the race completes after the end window
	// and ranks the robots as they stood.
	driver.runtime.Reconcile(nil, []string{a, b})
	result := driver.untilResult()
	if result.Status != ResultCompleted || result.EndReason != EndRaceComplete || result.WinnerID != nil || *result.Players[1].Rank != 1 || *result.Players[0].Rank != 2 || *result.Players[2].Rank != 2 {
		t.Fatalf("result %+v", result)
	}
	if err := ValidateResult(result, raceSnapshot(driver.runtime.session)); err != nil {
		t.Fatal(err)
	}
}

// A race without inputs runs to its end; the result is proposed once, ranks
// the robots, survives storage and becomes the committed frame's winner.
func TestRuntimeResult(t *testing.T) {
	driver := newDriver(t, 3)
	driver.bindAll()
	result := driver.untilResult()
	driver.ticks(5)
	if len(driver.results) != 1 || driver.frame.Phase != PhaseFinalizing {
		t.Fatalf("results %d, phase %s", len(driver.results), driver.frame.Phase)
	}
	session := raceSnapshot(driver.runtime.session)
	if err := ValidateResult(result, session); err != nil {
		t.Fatal(err)
	}
	if result.Status != ResultCompleted || result.StartedAt == nil || !result.StartedAt.Equal(session.StartedAt) ||
		result.EndedAt.Before(session.StartedAt.Add(time.Duration(result.DurationSeconds*float64(time.Second)))) {
		t.Fatalf("result %+v", result)
	}
	stored, err := EncodeMatchResult(result)
	if err != nil {
		t.Fatal(err)
	}
	game := Module()
	session.UpdatedAt = result.EndedAt
	final, err := game.FinalizeResult(stored, session)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMatchResult(final)
	if err != nil || !reflect.DeepEqual(decoded.Players, result.Players) {
		t.Fatalf("stored result: %v", err)
	}
	committed := driver.runtime.CommittedFrame(decoded)
	snapshot := SnapshotForConnection(committed, "", "", nil)
	if committed.Phase != PhaseEnded || !reflect.DeepEqual(snapshot.WinnerID, result.WinnerID) {
		t.Fatalf("committed frame %s %v", committed.Phase, snapshot.WinnerID)
	}
	// Tampering is refused.
	for name, change := range map[string]func(*FausSwitchResultModel){
		"rank":        func(result *FausSwitchResultModel) { *result.Players[0].Rank = 9 },
		"winner":      func(result *FausSwitchResultModel) { result.WinnerID = &result.Players[0].PlayerID },
		"reason":      func(result *FausSwitchResultModel) { result.EndReason = EndCancelledByUser },
		"duration":    func(result *FausSwitchResultModel) { result.DurationSeconds = 1.0 / 3 },
		"distance":    func(result *FausSwitchResultModel) { result.Players[1].Distance = -1 },
		"double stop": func(result *FausSwitchResultModel) { result.Players[0].FinishedAtTick = new(int64) },
		"player":      func(result *FausSwitchResultModel) { result.Players[2].PlayerID = "stranger" },
		"roster":      func(result *FausSwitchResultModel) { result.Players = result.Players[:2] },
	} {
		var copied FausSwitchResultModel
		encoded, _ := json.Marshal(result)
		_ = json.Unmarshal(encoded, &copied)
		if copied.Players[0].Rank == nil {
			copied.Players[0].Rank = new(int)
		}
		change(&copied)
		if ValidateResult(copied, session) == nil {
			t.Fatalf("%s: tampered result accepted", name)
		}
	}
}

func TestRuntimeCancellations(t *testing.T) {
	driver := newDriver(t, 2)
	driver.ticks(30)
	driver.now = driver.now.Add(maximumCatchUp + time.Millisecond)
	if err := driver.runtime.Advance(driver.now); err == nil {
		t.Fatal("a starved scheduler goes on")
	}
	driver.drain()
	result := driver.results[0]
	if result.Status != ResultCancelled || result.EndReason != EndRuntimeOverloaded || result.Players[0].Rank != nil || result.DurationSeconds != 0.25 {
		t.Fatalf("overloaded: %+v", result)
	}
	if err := ValidateResult(result, raceSnapshot(driver.runtime.session)); err != nil {
		t.Fatal(err)
	}
	roster, err := RecoveryCancellationResult(raceSnapshot(driver.runtime.session), EndRecoveryFailed, driver.now)
	if err != nil || len(roster.Players) != 2 || roster.Players[0].Distance != 0 {
		t.Fatalf("roster cancellation: %+v %v", roster, err)
	}
}

func checkpointOf(t *testing.T, runtime *Runtime) (json.RawMessage, module.Checkpoint) {
	t.Helper()
	checkpoint := runtime.Checkpoint()
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	return payload, *encodeCheckpoint(&checkpoint)
}

func restoreDriver(t *testing.T, payload json.RawMessage, header module.Checkpoint, session gameDomain.SessionSnapshot, now time.Time) (*raceDriver, error) {
	t.Helper()
	restored, err := Module().RestoreRuntime(payload, module.RestoreParams{Session: session, Epoch: 8, Now: now, Stored: header})
	if err != nil {
		return nil, err
	}
	return attachDriver(t, restored.(tickRuntime).Runtime, 8, now), nil
}

// A restored race goes on exactly as the original: same world, same frames,
// same flips; no control survives, and no flip reaches back before the
// restored tick.
func TestRuntimeCheckpointRestore(t *testing.T) {
	original := newDriver(t, 5)
	original.bindAll()
	original.ticks(150)
	_, _ = original.flip(playerID(2), original.tick()-5)
	original.ticks(30)
	original.runtime.Reconcile([]string{playerID(0), playerID(1), playerID(2), playerID(3)}, []string{playerID(0), playerID(1), playerID(2), playerID(3), playerID(4)})
	original.advance()
	payload, header := checkpointOf(t, original.runtime)
	session := raceSnapshot(original.runtime.session)
	restored, err := restoreDriver(t, payload, header, session, original.now)
	if err != nil {
		t.Fatal(err)
	}
	if restored.world() != original.world() || restored.frame.Phase != PhasePlaying || restored.frame.Controls[0].Connected ||
		restored.frame.World.Players[4].IsAlive || restored.frame.Controls[2].LastProcessedInputSequence != 1 {
		t.Fatal("restored race differs")
	}
	recovery := restored.runtime.RecoveryFrame()
	if recovery.Phase != PhaseRecovering {
		t.Fatal("recovery frame")
	}
	for seat := range 4 {
		restored.bind(playerID(seat))
	}
	original.ticks(int(restored.tick() - original.tick()))
	_, _ = original.flip(playerID(1), original.tick())
	_, _ = restored.flip(playerID(1), restored.tick())
	original.ticks(60)
	restored.ticks(60)
	if restored.world() != original.world() {
		t.Fatal("the restored race diverged")
	}
	if !reflect.DeepEqual(restored.frame.World, original.frame.World) {
		t.Fatal("frames diverged")
	}
	// A finished race restores with its proposed result.
	result := original.untilResult()
	payload, header = checkpointOf(t, original.runtime)
	finished, err := restoreDriver(t, payload, header, session, original.now)
	if err != nil || len(finished.results) != 1 || !reflect.DeepEqual(finished.results[0], result) || finished.frame.Phase != PhaseFinalizing {
		t.Fatalf("finished race: %v", err)
	}
	// A cancel reason ends a race without a result yet.
	early := newDriver(t, 2)
	early.ticks(10)
	payload, header = checkpointOf(t, early.runtime)
	cancelled, err := Module().RestoreRuntime(payload, module.RestoreParams{Session: raceSnapshot(early.runtime.session), Epoch: 8, Now: early.now, Stored: header, CancelReason: string(EndRecoveryFailed)})
	if err != nil {
		t.Fatal(err)
	}
	stored, ok, err := cancelled.TakeResult()
	if !ok || err != nil || stored.EndReason != string(EndRecoveryFailed) {
		t.Fatalf("cancelled restore: %v %v", ok, err)
	}
}

func TestRuntimeRestoreRejectsTamperedCheckpoints(t *testing.T) {
	driver := newDriver(t, 3)
	driver.bindAll()
	driver.ticks(100)
	payload, header := checkpointOf(t, driver.runtime)
	session := raceSnapshot(driver.runtime.session)
	if _, err := restoreDriver(t, payload, header, session, driver.now); err != nil {
		t.Fatal(err)
	}
	mutate := func(change func(*RuntimeCheckpoint)) json.RawMessage {
		var checkpoint RuntimeCheckpoint
		if err := json.Unmarshal(payload, &checkpoint); err != nil {
			t.Fatal(err)
		}
		change(&checkpoint)
		changed, _ := json.Marshal(checkpoint)
		return changed
	}
	for name, change := range map[string]func(*RuntimeCheckpoint){
		"schema": func(checkpoint *RuntimeCheckpoint) { checkpoint.SchemaVersion = 2 },
		"same color": func(checkpoint *RuntimeCheckpoint) {
			checkpoint.Session.Players[1].Color = checkpoint.Session.Players[0].Color
		},
		"left runner":   func(checkpoint *RuntimeCheckpoint) { checkpoint.Controls[0].Left = true },
		"control order": func(checkpoint *RuntimeCheckpoint) { checkpoint.Controls[0].PlayerID = playerID(1) },
		"no outcome":    func(checkpoint *RuntimeCheckpoint) { checkpoint.ProposedResult = &FausSwitchResultModel{} },
		"in a wall":     func(checkpoint *RuntimeCheckpoint) { checkpoint.Race.Runners[0].Y = -1000 },
		"lost robot":    func(checkpoint *RuntimeCheckpoint) { checkpoint.Race.Runners = checkpoint.Race.Runners[:2] },
		"remainder":     func(checkpoint *RuntimeCheckpoint) { checkpoint.Remainder = int64(time.Second) },
	} {
		if _, err := restoreDriver(t, mutate(change), header, session, driver.now); err == nil {
			t.Fatalf("%s: tampered checkpoint restored", name)
		}
	}
	other := session
	other.SessionID = "another-session"
	if _, err := restoreDriver(t, payload, header, other, driver.now); err == nil {
		t.Fatal("a checkpoint of another session restored")
	}
	if _, err := Module().RestoreRuntime(payload, module.RestoreParams{Session: session, Epoch: 7, Now: driver.now, Stored: header}); err == nil {
		t.Fatal("a checkpoint of the same epoch restored")
	}
}

// Only a change a lost checkpoint would show is critical.
func TestRuntimeCriticalKey(t *testing.T) {
	driver := newDriver(t, 2)
	key := driver.runtime.Checkpoint().CriticalKey()
	driver.ticks(10)
	if driver.runtime.Checkpoint().CriticalKey() != key {
		t.Fatal("motion is not critical")
	}
	driver.bind(playerID(0))
	bound := driver.runtime.Checkpoint().CriticalKey()
	if bound == key {
		t.Fatal("a control handover is critical")
	}
	_, _ = driver.flip(playerID(0), driver.tick())
	driver.advance()
	if driver.runtime.Checkpoint().CriticalKey() != bound {
		t.Fatal("a flip is not critical")
	}
	driver.runtime.Reconcile([]string{playerID(0)}, []string{playerID(0), playerID(1)})
	if driver.runtime.Checkpoint().CriticalKey() == bound {
		t.Fatal("a leaver is critical")
	}
}

// The view renders each connection's projection, splicing in only its own
// robot's generation, and an eliminated robot's player keeps it while the
// race runs, so a rescuing late flip still passes the gateway.
func TestRuntimeFrameView(t *testing.T) {
	driver := newDriver(t, 3)
	grants := map[string]FausSwitchControlGrantedModel{}
	for seat := range 3 {
		grants[playerID(seat)] = driver.bind(playerID(seat))
	}
	driver.runtime.mutex.Lock()
	_ = driver.runtime.race.eliminate(1, EliminationForfeit)
	driver.runtime.mutex.Unlock()
	driver.ticks(int(ControlHeartbeatInterval / oneTick))
	game := Module()
	payload, err := json.Marshal(driver.frame)
	if err != nil {
		t.Fatal(err)
	}
	view, err := game.PrepareFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	header := view.Header()
	if !header.ControlledBy(playerID(1), "connection-"+playerID(1)) || header.ControlledBy(playerID(1), "elsewhere") {
		t.Fatal("an eliminated robot's player keeps control")
	}
	for seat := range 3 {
		id := playerID(seat)
		grant := grants[id]
		viewer := module.Viewer{PlayerID: id, ConnectionID: "connection-" + id, Grant: &grant}
		rendered, err := view.Render(viewer)
		if err != nil {
			t.Fatal(err)
		}
		expected, _ := json.Marshal(SnapshotForConnection(driver.frame, id, "connection-"+id, &grant))
		if string(rendered) != string(expected) || view.ViewKey(viewer) == "" {
			t.Fatalf("%s: view differs from its projection", id)
		}
		if strings.Count(string(rendered), grant.ControlGeneration) != 1 {
			t.Fatalf("%s: generation shown %d times", id, strings.Count(string(rendered), grant.ControlGeneration))
		}
	}
	spectator, _ := view.Render(module.Viewer{PlayerID: "outsider", ConnectionID: "x"})
	var shared FausSwitchSnapshotModel
	if err := json.Unmarshal(spectator, &shared); err != nil {
		t.Fatal(err)
	}
	for _, runner := range shared.Runners {
		if runner.ControlGeneration != nil || runner.UsedBoosts == nil || runner.UsedSlows == nil {
			t.Fatal("a spectator sees no generation, and pad lists are never null")
		}
	}
	if shared.Tick != driver.frame.World.Tick || shared.ElapsedSeconds != float64(shared.Tick)/PhysicsRateHz || shared.CountdownEndsAt != nil || shared.WinnerID != nil {
		t.Fatalf("snapshot header %+v", shared)
	}
	// A player who left loses control for good.
	driver.runtime.Reconcile([]string{playerID(0), playerID(2)}, []string{playerID(0), playerID(1), playerID(2)})
	driver.advance()
	payload, _ = json.Marshal(driver.frame)
	view, _ = game.PrepareFrame(payload)
	if view.Header().Alive(playerID(1)) {
		t.Fatal("a leaver is not alive")
	}
}

func TestRuntimeDoesNotAllocateWhenIdle(t *testing.T) {
	driver := newDriver(t, 8)
	driver.bindAll()
	driver.runtime.mutex.Lock()
	defer driver.runtime.mutex.Unlock()
	allocations := testing.AllocsPerRun(100, func() {
		runtime := driver.runtime
		_ = runtime.race.advance(2)
		runtime.applyPending(driver.now, &module.RuntimeObservation{})
	})
	if allocations != 0 {
		t.Fatalf("a step without flips allocates %v times", allocations)
	}
}

// A scheduler step that publishes no frame allocates nothing either.
func TestRuntimeStepBetweenFramesDoesNotAllocate(t *testing.T) {
	driver := newDriver(t, 8)
	driver.bindAll()
	runtime := driver.runtime
	runtime.nextFrame = driver.now.Add(time.Hour)
	allocations := testing.AllocsPerRun(100, func() {
		driver.now = driver.now.Add(time.Second / SchedulingRateHz)
		if err := runtime.Advance(driver.now); err != nil {
			t.Fatal(err)
		}
	})
	if allocations != 0 || len(runtime.frames) != 0 {
		t.Fatalf("a quiet step allocates %v times", allocations)
	}
}

// A late flip that rescues a stopped robot shows in the frame of the very
// step that applies it: the status change is critical, so the frame does not
// wait for the snapshot interval. The flip reaches at most MaximumRewindTicks
// back and the robot was running at the flip's tick, so that frame is at most
// MaximumRewindTicks-1 ticks after the robot's end, and the robot is running
// with finishedAtTick, eliminatedAtTick and eliminationReason null.
// (Client report §3.1: the client bounds a rescue by tick - end ≤ 36.)
func TestRuntimeRescueShowsInTheStepThatAppliesIt(t *testing.T) {
	// Without flips seat 0 crashes at end; find the latest flip tick that
	// rescues it, then send that flip as late as the rewind window allows.
	plain := newDriver(t, 2)
	plain.bindAll()
	for plain.world()[0].status == RunnerRunning {
		plain.advance()
	}
	end := plain.world()[0].crashedAtTick
	for flipTick := end - 1; flipTick > end-MaximumRewindTicks; flipTick-- {
		driver := newDriver(t, 2)
		driver.bindAll()
		// Flips apply after the step's ticks, so a flip one step from now
		// reaches back MaximumRewindTicks from the tick that step reaches.
		driver.ticks(int(flipTick + MaximumRewindTicks - 1 - driver.tick()))
		before := SnapshotForConnection(driver.frame, playerID(0), "", nil)
		if before.Runners[0].Status != RunnerCrashed || *before.Runners[0].EliminatedAtTick != end {
			t.Fatalf("before the flip the last frame shows %+v", before.Runners[0])
		}
		if _, err := driver.flip(playerID(0), flipTick); err != nil {
			t.Fatal(err)
		}
		driver.advance()
		if driver.world()[0].status != RunnerRunning {
			continue // this flip does not save the robot; try an earlier one
		}
		after := SnapshotForConnection(driver.frame, playerID(0), "", nil)
		rescued := after.Runners[0]
		if after.Tick != driver.tick() || after.StateSequence <= before.StateSequence {
			t.Fatalf("the rescue frame is tick %d sequence %d; the step reached %d (last frame sequence %d)", after.Tick, after.StateSequence, driver.tick(), before.StateSequence)
		}
		if rescued.Status != RunnerRunning || rescued.FinishedAtTick != nil || rescued.EliminatedAtTick != nil || rescued.EliminationReason != nil {
			t.Fatalf("rescued robot %+v", rescued)
		}
		encoded, _ := json.Marshal(rescued)
		for _, field := range []string{`"finishedAtTick":null`, `"eliminatedAtTick":null`, `"eliminationReason":null`} {
			if !strings.Contains(string(encoded), field) {
				t.Fatalf("rescued robot on the wire lacks %s: %s", field, encoded)
			}
		}
		if gap := after.Tick - end; gap > MaximumRewindTicks-1 {
			t.Fatalf("rescue shows %d ticks after the end", gap)
		}
		t.Logf("crashed at %d, rescued by a flip for %d applied at %d: first running frame at tick %d (tick - end = %d)", end, flipTick, after.Tick, after.Tick, after.Tick-end)
		return
	}
	t.Fatalf("no flip within the rewind window rescues the robot that crashes at %d", end)
}
