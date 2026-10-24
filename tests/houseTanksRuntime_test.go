package tests

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"testing"
	"time"

	"houseflowApi/internal/application/game/games/houseTanks"
	"houseflowApi/internal/application/game/module"
)

// tickDuration advances exactly one physics tick per Advance.
const tanksTickDuration = time.Second/houseTanks.PhysicsRateHz + 1

// tanksDriver plays a runtime tick by tick: it keeps bound players alive with
// heartbeats and drains every publication so the mailboxes never fill.
type tanksDriver struct {
	t           *testing.T
	runtime     *houseTanks.Runtime
	epoch       int64
	now         time.Time
	grants      map[string]houseTanks.HouseTanksControlGrantedModel
	connections map[string]string
	sequences   map[string]int64
	events      []houseTanks.RuntimeEvent
	frame       houseTanks.RuntimeFrame
	silent      map[string]bool
	results     []houseTanks.HouseTanksResultModel
}

func newTanksDriver(t *testing.T, ids ...string) *tanksDriver {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	session := houseTanks.RuntimeSession{SessionID: "contract-session", HouseID: "contract-house", StartedAt: now}
	for _, id := range ids {
		session.Players = append(session.Players, houseTanks.RuntimePlayer{PlayerID: id, DisplayName: "Player " + id})
	}
	runtime, err := houseTanks.NewRuntime(session, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	return attachTanksDriver(t, runtime, 7, now)
}

func attachTanksDriver(t *testing.T, runtime *houseTanks.Runtime, epoch int64, now time.Time) *tanksDriver {
	driver := &tanksDriver{t: t, runtime: runtime, epoch: epoch, now: now, grants: map[string]houseTanks.HouseTanksControlGrantedModel{},
		connections: map[string]string{}, sequences: map[string]int64{}, silent: map[string]bool{}}
	driver.drain()
	return driver
}

func (driver *tanksDriver) drain() {
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

func (driver *tanksDriver) input(id, kind string) houseTanks.RuntimeInput {
	grant := driver.grants[id]
	return houseTanks.RuntimeInput{Kind: kind, PlayerID: id, ConnectionID: driver.connections[id], RuntimeEpoch: driver.epoch, ControlGeneration: grant.ControlGeneration, CreatedAt: driver.now}
}

func (driver *tanksDriver) bind(id, connection string) houseTanks.HouseTanksControlGrantedModel {
	driver.t.Helper()
	input := houseTanks.RuntimeInput{Kind: houseTanks.InputBind, PlayerID: id, ConnectionID: connection, RuntimeEpoch: driver.epoch, CreatedAt: driver.now}
	if err := driver.runtime.Submit(input, driver.now); err != nil {
		driver.t.Fatalf("bind %s: %v", id, err)
	}
	driver.events = nil
	driver.advance()
	for _, event := range driver.events {
		if event.Grant != nil && event.ConnectionID == connection {
			driver.grants[id], driver.connections[id] = *event.Grant, connection
			return *event.Grant
		}
	}
	driver.t.Fatalf("no grant for %s", id)
	return houseTanks.HouseTanksControlGrantedModel{}
}

func (driver *tanksDriver) advance() {
	driver.t.Helper()
	driver.now = driver.now.Add(tanksTickDuration)
	if err := driver.runtime.Advance(driver.now); err != nil {
		driver.t.Fatal(err)
	}
	driver.drain()
}

// ticks advances n ticks, with a heartbeat from every bound, non-silent
// player each second.
func (driver *tanksDriver) ticks(n int) {
	driver.t.Helper()
	for index := range n {
		if index%houseTanks.PhysicsRateHz == 0 {
			for id := range driver.grants {
				if !driver.silent[id] {
					_ = driver.runtime.Submit(driver.input(id, houseTanks.InputHeartbeat), driver.now)
				}
			}
		}
		driver.advance()
	}
}

// flush advances until the next published frame: motion and ACKs ride the
// snapshot interval, not every tick.
func (driver *tanksDriver) flush() {
	driver.t.Helper()
	sequence := driver.frame.StateSequence
	for range houseTanks.PhysicsRateHz {
		if driver.frame.StateSequence > sequence {
			return
		}
		driver.ticks(1)
	}
	driver.t.Fatal("no frame published")
}

func (driver *tanksDriver) press(id string, pressed bool, clientTick int64, heading float64) error {
	driver.sequences[id]++
	input := driver.input(id, houseTanks.InputPress)
	input.InputSequence, input.Pressed, input.ClientTick, input.Heading = driver.sequences[id], pressed, clientTick, heading
	return driver.runtime.Submit(input, driver.now)
}

func (driver *tanksDriver) ready(id string, ready bool) error {
	input := driver.input(id, houseTanks.InputRoundReady)
	input.Ready = ready
	return driver.runtime.Submit(input, driver.now)
}

func (driver *tanksDriver) read(inspect func(match *houseTanks.Match)) {
	driver.runtime.WithMatch(inspect)
}

func (driver *tanksDriver) match() houseTanks.Match {
	var copied houseTanks.Match
	driver.read(func(match *houseTanks.Match) {
		encoded, _ := json.Marshal(match)
		_ = json.Unmarshal(encoded, &copied)
	})
	return copied
}

// untilRoundPhase advances until the match reaches the round phase.
func (driver *tanksDriver) untilRoundPhase(phase houseTanks.RoundPhase) {
	driver.t.Helper()
	for range houseTanks.RoundReadyWindowTicks + houseTanks.RoundCountdownTicks {
		if driver.match().RoundPhase == phase {
			return
		}
		driver.ticks(1)
	}
	driver.t.Fatalf("never reached %s", phase)
}

func (driver *tanksDriver) destroyAllBut(survivor string) {
	driver.read(func(match *houseTanks.Match) {
		for index := range match.World.Tanks {
			if match.World.Tanks[index].PlayerID != survivor {
				match.World.Tanks[index].Active, match.World.Tanks[index].Armor = false, 0
			}
		}
	})
}

func (driver *tanksDriver) firedAngles(id string) []float64 {
	var angles []float64
	driver.read(func(match *houseTanks.Match) {
		for _, event := range match.Events {
			if event.Kind == houseTanks.EventFired && event.PlayerID == id {
				angles = append(angles, *event.Angle)
			}
		}
	})
	return angles
}

func TestHouseTanksRuntimeControl(t *testing.T) {
	driver := newTanksDriver(t, "a", "b", "c")
	if driver.frame.Phase != houseTanks.PhasePlaying || *driver.frame.World.RoundPhase != houseTanks.RoundCountdown || driver.frame.World.Tanks[0].Controllable {
		t.Fatal("a fresh match counts down with stationary tanks")
	}
	first := driver.bind("a", "connection-a")
	driver.bind("c", "connection-c")
	driver.ticks(1)
	if !driver.frame.World.Tanks[0].Controllable || !driver.frame.Controls[0].Connected || driver.frame.Controls[0].ConnectionID != "connection-a" {
		t.Fatal("a binding makes the tank controllable")
	}
	// A newer connection takes the controls over; the old one is fenced out.
	second := driver.bind("a", "connection-b")
	if second.ControlGeneration == first.ControlGeneration {
		t.Fatal("a takeover issues a new generation")
	}
	stale := driver.input("a", houseTanks.InputPress)
	stale.ConnectionID, stale.ControlGeneration, stale.InputSequence = "connection-a", first.ControlGeneration, 1
	if err := driver.runtime.Submit(stale, driver.now); !errors.Is(err, module.ErrControlRequired) {
		t.Fatalf("old controller = %v", err)
	}
	// Silence past the control timeout makes a stationary target; a new
	// binding gives control back.
	driver.silent["c"] = true
	driver.ticks(int(houseTanks.ControlTimeout/time.Second)*houseTanks.PhysicsRateHz + 1)
	if driver.frame.World.Tanks[2].Controllable || driver.frame.Controls[2].Connected || !driver.frame.World.Tanks[0].Controllable {
		t.Fatal("a silent controller times out into a stationary target")
	}
	driver.silent["c"] = false
	driver.bind("c", "connection-c2")
	driver.ticks(1)
	if !driver.frame.World.Tanks[2].Controllable {
		t.Fatal("rebinding restores control")
	}
	// A disconnect frees the controls at once.
	if err := driver.runtime.Submit(driver.input("a", houseTanks.InputDisconnect), driver.now); err != nil {
		t.Fatal(err)
	}
	driver.ticks(1)
	if driver.frame.World.Tanks[0].Controllable || driver.frame.Controls[0].Connected {
		t.Fatal("disconnect")
	}
	// Leaving is permanent: no rebinding, isAlive false in the header.
	driver.runtime.Reconcile([]string{"a", "c"}, []string{"a", "b", "c"})
	driver.ticks(1)
	if driver.frame.World.Players[1].IsAlive || !driver.frame.World.Players[1].HasLeft || !driver.frame.World.Players[0].IsAlive {
		t.Fatal("a player who left loses control rights for the match")
	}
	bind := houseTanks.RuntimeInput{Kind: houseTanks.InputBind, PlayerID: "b", ConnectionID: "connection-x", RuntimeEpoch: 7, CreatedAt: driver.now}
	if err := driver.runtime.Submit(bind, driver.now); !errors.Is(err, module.ErrPlayerEliminated) {
		t.Fatalf("left player bind = %v", err)
	}
	for name, check := range map[string]struct {
		change func(*houseTanks.RuntimeInput)
		want   error
	}{
		"stranger":      {func(input *houseTanks.RuntimeInput) { input.PlayerID = "stranger" }, module.ErrPlayerNotFound},
		"old epoch":     {func(input *houseTanks.RuntimeInput) { input.RuntimeEpoch = 6 }, module.ErrStaleInput},
		"old input":     {func(input *houseTanks.RuntimeInput) { input.CreatedAt = driver.now.Add(-2 * time.Second) }, module.ErrInputExpired},
		"no connection": {func(input *houseTanks.RuntimeInput) { input.ConnectionID = "" }, module.ErrInvalidInput},
	} {
		input := driver.input("c", houseTanks.InputHeartbeat)
		check.change(&input)
		if err := driver.runtime.Submit(input, driver.now); !errors.Is(err, check.want) {
			t.Fatalf("%s = %v", name, err)
		}
	}
}

func TestHouseTanksRuntimeInputs(t *testing.T) {
	driver := newTanksDriver(t, "a", "b")
	driver.bind("a", "connection-a")
	driver.bind("b", "connection-b")
	// Presses around the round boundary are acknowledged and ignored.
	if err := driver.press("a", true, 0, 0); err != nil {
		t.Fatal(err)
	}
	driver.flush()
	if driver.frame.Controls[0].LastProcessedInputSequence != 1 || driver.frame.World.Tanks[0].IsDriving || len(driver.firedAngles("a")) != 0 {
		t.Fatal("a countdown press is acknowledged without effect")
	}
	if err := driver.press("a", false, 0, 0); err != nil {
		t.Fatal(err)
	}
	driver.untilRoundPhase(houseTanks.RoundPlaying)
	// A tap inside one scheduler step still fires and drives for a tick.
	tick := driver.match().Tick
	if err := driver.press("a", true, tick, 0); err != nil {
		t.Fatal(err)
	}
	if err := driver.press("a", false, tick, 0); err != nil {
		t.Fatal(err)
	}
	driver.ticks(1)
	if tank, _ := driver.match().World.TankOf("a"); !tank.Driving || len(driver.firedAngles("a")) != 1 {
		t.Fatal("the press of a tap applies first")
	}
	driver.ticks(1)
	if tank, _ := driver.match().World.TankOf("a"); tank.Driving {
		t.Fatal("the release of a tap applies on the next tick")
	}
	if driver.flush(); driver.frame.Controls[0].LastProcessedInputSequence != 4 {
		t.Fatal("both halves of the tap are acknowledged")
	}
	stale := driver.input("a", houseTanks.InputPress)
	stale.InputSequence, stale.Pressed = 2, true
	if err := driver.runtime.Submit(stale, driver.now); !errors.Is(err, module.ErrStaleInput) {
		t.Fatalf("stale press = %v", err)
	}
	// One player's queue holds eight presses; the rate limit is per second.
	for index := range houseTanks.PlayerQueueSize {
		if err := driver.press("b", index%2 == 0, tick, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := driver.press("b", true, tick, 0); !errors.Is(err, module.ErrInputRate) {
		t.Fatalf("queue overflow = %v", err)
	}
	for range 30 {
		driver.ticks(1)
		_ = driver.press("b", false, tick, 0)
	}
	if err := driver.press("b", false, tick, 0); !errors.Is(err, module.ErrInputRate) {
		t.Fatalf("rate limit = %v", err)
	}
	// A destroyed tank's presses are rejected; its owner still votes.
	driver.destroyAllBut("a")
	if err := driver.press("b", true, tick, 0); !errors.Is(err, module.ErrPlayerEliminated) {
		t.Fatalf("destroyed tank press = %v", err)
	}
	if err := driver.ready("b", true); !errors.Is(err, module.ErrInvalidInput) {
		t.Fatalf("vote while playing = %v", err)
	}
	driver.ticks(1)
	if *driver.frame.World.RoundPhase != houseTanks.RoundReady || driver.frame.World.Players[1].TankAlive || !driver.frame.World.Players[1].IsAlive {
		t.Fatal("the round ends; the loser keeps control rights")
	}
	if err := driver.ready("b", true); err != nil {
		t.Fatalf("destroyed tank's owner vote = %v", err)
	}
	if err := driver.ready("a", true); err != nil {
		t.Fatal(err)
	}
	driver.ticks(1)
	if *driver.frame.World.RoundPhase != houseTanks.RoundCountdown || driver.frame.World.RoundNumber != 2 {
		t.Fatal("both votes start round 2")
	}
}

func TestHouseTanksRuntimeLagCompensation(t *testing.T) {
	driver := newTanksDriver(t, "a", "b")
	driver.bind("a", "connection-a")
	driver.bind("b", "connection-b")
	driver.untilRoundPhase(houseTanks.RoundPlaying)
	rotations := map[int64]float64{}
	record := func(n int) {
		for range n {
			driver.ticks(1)
			match := driver.match()
			rotations[match.Tick] = match.World.Tanks[0].Rotation
		}
	}
	record(40)
	fire := func(clientTick int64, heading float64) float64 {
		t.Helper()
		before := len(driver.firedAngles("a"))
		if err := driver.press("a", true, clientTick, houseTanks.NormalizedAngle(heading)); err != nil {
			t.Fatal(err)
		}
		driver.ticks(1)
		angles := driver.firedAngles("a")
		if len(angles) != before+1 {
			t.Fatal("press did not fire")
		}
		_ = driver.press("a", false, clientTick, 0)
		record(houseTanks.FireCooldownTicks + 30)
		return angles[len(angles)-1]
	}
	same := func(label string, actual, expected float64) {
		t.Helper()
		if math.Abs(houseTanks.NormalizedAngle(actual-expected)) > 1e-9 {
			t.Fatalf("%s: fired at %v, want %v", label, actual, expected)
		}
	}
	// What the client saw 10 ticks ago is on the swept arc: the shot uses it.
	server := driver.match().Tick
	same("accepted", fire(server-10, rotations[server-10]), rotations[server-10])
	// A heading off the arc by more than the tolerance keeps the server's.
	server = driver.match().Tick
	direction := 1.0
	if rotations[server] < rotations[server-1] {
		direction = -1
	}
	same("off the arc", fire(server-10, rotations[server]+direction*0.2), rotations[server])
	// Inside the tolerance just past the arc's end is still accepted.
	server = driver.match().Tick
	same("tolerance", fire(server-10, rotations[server]+direction*0.03), rotations[server]+direction*0.03)
	// Older than the window, or from the future: the server's heading.
	server = driver.match().Tick
	same("too old", fire(server-30, rotations[server-30]), rotations[server])
	server = driver.match().Tick
	same("future", fire(server+5, rotations[server]+direction*0.02), rotations[server])
}

func TestHouseTanksRuntimeMatchResult(t *testing.T) {
	session := runningContractSession(t, houseTanks.Definition())
	runtimeSession := houseTanks.RuntimeSession{SessionID: session.SessionID, HouseID: session.HouseID, StartedAt: session.StartedAt,
		Players: []houseTanks.RuntimePlayer{{PlayerID: "contract-a", DisplayName: "A"}, {PlayerID: "contract-b", DisplayName: "B"}}}
	runtime, err := houseTanks.NewRuntime(runtimeSession, 3, session.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	driver := attachTanksDriver(t, runtime, 3, session.StartedAt)
	driver.bind("contract-a", "connection-a")
	driver.bind("contract-b", "connection-b")
	var proposed *houseTanks.HouseTanksResultModel
	for round := 1; proposed == nil && round <= houseTanks.RoundWinsNeeded+1; round++ {
		driver.untilRoundPhase(houseTanks.RoundPlaying)
		winner := "contract-a"
		if round == 2 {
			winner = "contract-b"
		}
		driver.destroyAllBut(winner)
		driver.now = driver.now.Add(tanksTickDuration)
		if err := runtime.Advance(driver.now); err != nil {
			t.Fatal(err)
		}
		select {
		case result := <-runtime.Results():
			proposed = &result
		default:
		}
		driver.drain()
		if proposed == nil {
			_ = driver.ready("contract-a", true)
			_ = driver.ready("contract-b", true)
		}
	}
	if proposed == nil {
		t.Fatal("three wins must end the match")
	}
	if driver.frame.Phase != houseTanks.PhaseFinalizing || *driver.frame.World.RoundPhase != houseTanks.MatchEnded ||
		houseTanks.SnapshotForConnection(driver.frame, "", "", nil).ChampionID != nil {
		t.Fatal("the champion stays hidden until the result commits")
	}
	expectedMap, _ := houseTanks.MapByID(driver.match().MapID)
	if proposed.Status != houseTanks.ResultCompleted || proposed.EndReason != houseTanks.EndChampionDecided || *proposed.ChampionID != "contract-a" || proposed.RoundsPlayed != 4 ||
		*proposed.Players[0].FinalRank != 1 || *proposed.Players[1].FinalRank != 2 || proposed.Players[0].RoundsWon != 3 || *proposed.MapID != expectedMap.ID {
		t.Fatalf("result = %+v", proposed)
	}
	game := houseTanks.Module()
	stored, err := houseTanks.EncodeMatchResult(*proposed)
	if err != nil {
		t.Fatal(err)
	}
	finalized, err := game.FinalizeResult(stored, session)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := houseTanks.DecodeMatchResult(finalized)
	if err != nil {
		t.Fatal(err)
	}
	committed := runtime.CommittedFrame(decoded)
	if snapshot := houseTanks.SnapshotForConnection(committed, "", "", nil); committed.Phase != houseTanks.PhaseEnded || snapshot.ChampionID == nil || *snapshot.ChampionID != "contract-a" {
		t.Fatal("the committed frame names the champion")
	}
	// Results the session cannot have produced are refused.
	for name, change := range map[string]func(*houseTanks.HouseTanksResultModel){
		"two champions": func(result *houseTanks.HouseTanksResultModel) { result.Players[1].RoundsWon = 3 },
		"rank":          func(result *houseTanks.HouseTanksResultModel) { rank := 1; result.Players[1].FinalRank = &rank },
		"wins":          func(result *houseTanks.HouseTanksResultModel) { result.RoundsPlayed = 2 },
		"map": func(result *houseTanks.HouseTanksResultModel) {
			other := houseTanks.MapBasement
			result.MapID = &other
		},
		"cancelled champion": func(result *houseTanks.HouseTanksResultModel) {
			result.Status, result.EndReason = houseTanks.ResultCancelled, houseTanks.EndSessionExpired
		},
		"stranger": func(result *houseTanks.HouseTanksResultModel) { result.Players[1].PlayerID = "stranger" },
		"too long": func(result *houseTanks.HouseTanksResultModel) { result.DurationSeconds = 301 },
	} {
		tampered := decoded
		tampered.Players = append([]houseTanks.HouseTanksPlayerResultModel(nil), decoded.Players...)
		change(&tampered)
		if houseTanks.ValidateResult(tampered, session) == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	// A recovery that lost everything keeps the map and invents no score.
	cancelled, err := houseTanks.RecoveryCancellationResult(session, houseTanks.EndRecoveryFailed, session.StartedAt.Add(time.Minute))
	if err != nil || *cancelled.MapID != expectedMap.ID || cancelled.Players[0].RoundsWon != 0 || cancelled.Players[0].FinalRank != nil {
		t.Fatalf("recovery result = %+v %v", cancelled, err)
	}
}

func TestHouseTanksRuntimeInsufficientPlayers(t *testing.T) {
	driver := newTanksDriver(t, "a", "b", "c")
	driver.bind("a", "connection-a")
	timing := houseTanks.DefaultMatchTiming()
	for range timing.PresenceGraceTicks + timing.InsufficientGraceTicks + 2 {
		if driver.frame.Phase == houseTanks.PhaseFinalizing {
			break
		}
		driver.ticks(1)
	}
	if driver.frame.Phase != houseTanks.PhaseFinalizing || driver.match().EndReason != houseTanks.EndInsufficientPlayers || len(driver.results) != 1 {
		t.Fatal("one bound player of three must end the match")
	}
	result := driver.results[0]
	if result.Status != houseTanks.ResultCancelled || result.EndReason != houseTanks.EndInsufficientPlayers || result.ChampionID != nil || result.Players[0].FinalRank != nil {
		t.Fatalf("result = %+v", result)
	}
	if err := driver.press("a", true, 0, 0); !errors.Is(err, module.ErrFinished) {
		t.Fatalf("press after the end = %v", err)
	}
}

func TestHouseTanksRuntimeCheckpointRestore(t *testing.T) {
	driver := newTanksDriver(t, "a", "b", "c")
	for _, id := range []string{"a", "b", "c"} {
		driver.bind(id, "connection-"+id)
	}
	driver.untilRoundPhase(houseTanks.RoundPlaying)
	_ = driver.press("a", true, driver.match().Tick, 0)
	driver.ticks(30)
	checkpoint := driver.runtime.Checkpoint()
	// Motion is not critical; a binding change is.
	driver.ticks(10)
	if driver.runtime.Checkpoint().CriticalKey() != checkpoint.CriticalKey() {
		t.Fatal("motion changed the critical key")
	}
	driver.bind("b", "connection-b2")
	if driver.runtime.Checkpoint().CriticalKey() == checkpoint.CriticalKey() {
		t.Fatal("a takeover must change the critical key")
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	var stored houseTanks.RuntimeCheckpoint
	if err := json.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	restoredAt := driver.now.Add(time.Second)
	restored, err := houseTanks.RestoreRuntime(stored, 8, restoredAt)
	if err != nil {
		t.Fatal(err)
	}
	recovering := restored.RecoveryFrame()
	if recovering.Phase != houseTanks.PhaseRecovering || recovering.RuntimeEpoch != 8 || *recovering.World.RoundPhase != houseTanks.RoundPlaying {
		t.Fatalf("recovery frame = %s", recovering.Phase)
	}
	var match houseTanks.Match
	_ = json.Unmarshal(stored.Match, &match)
	if recovering.World.Tick != match.Tick {
		t.Fatal("no ticks are spent on the ownership gap")
	}
	for index, tank := range recovering.World.Tanks {
		if tank.Controllable || tank.IsDriving || recovering.Controls[index].Connected {
			t.Fatal("restored tanks wait as stationary targets for their players")
		}
	}
	after := attachTanksDriver(t, restored, 8, restoredAt)
	after.bind("a", "connection-a3")
	after.ticks(int(houseTanks.ControlTimeout/time.Second) * houseTanks.PhysicsRateHz)
	if after.match().Status != "" {
		t.Fatal("players count as present while they rebind")
	}
	for name, change := range map[string]func(*houseTanks.RuntimeCheckpoint){
		"schema":   func(value *houseTanks.RuntimeCheckpoint) { value.SchemaVersion = 2 },
		"epoch":    func(value *houseTanks.RuntimeCheckpoint) { value.Epoch = 8 },
		"controls": func(value *houseTanks.RuntimeCheckpoint) { value.Controls = value.Controls[:2] },
		"seats": func(value *houseTanks.RuntimeCheckpoint) {
			value.Session.Players = append([]houseTanks.RuntimePlayer(nil), value.Session.Players...)
			value.Session.Players[0], value.Session.Players[1] = value.Session.Players[1], value.Session.Players[0]
		},
		"match":     func(value *houseTanks.RuntimeCheckpoint) { value.Match = json.RawMessage(`{}`) },
		"proposed":  func(value *houseTanks.RuntimeCheckpoint) { value.ProposedResult = &houseTanks.HouseTanksResultModel{} },
		"remainder": func(value *houseTanks.RuntimeCheckpoint) { value.Remainder = -1 },
	} {
		var tampered houseTanks.RuntimeCheckpoint
		_ = json.Unmarshal(encoded, &tampered)
		change(&tampered)
		if _, err := houseTanks.RestoreRuntime(tampered, 8, restoredAt); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestHouseTanksRuntimeFrameView(t *testing.T) {
	driver := newTanksDriver(t, "a", "b", "c")
	grants := map[string]houseTanks.HouseTanksControlGrantedModel{}
	for _, id := range []string{"a", "b", "c"} {
		grants[id] = driver.bind(id, "connection-"+id)
	}
	driver.untilRoundPhase(houseTanks.RoundPlaying)
	_ = driver.press("a", true, driver.match().Tick, 0)
	driver.ticks(int(houseTanks.SnapshotRateHz))
	game := houseTanks.Module()
	render := func(frame houseTanks.RuntimeFrame, viewer module.Viewer) (json.RawMessage, module.FrameView) {
		t.Helper()
		payload, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		view, err := game.PrepareFrame(payload)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(view.Header(), decodeFrameHeader(t, payload)) {
			t.Fatal("view header differs from the payload")
		}
		rendered, err := view.Render(viewer)
		if err != nil {
			t.Fatal(err)
		}
		return rendered, view
	}
	viewerOf := func(id string) module.Viewer {
		grant := grants[id]
		return module.Viewer{PlayerID: id, ConnectionID: "connection-" + id, Grant: &grant}
	}
	for _, id := range []string{"a", "b"} {
		rendered, view := render(driver.frame, viewerOf(id))
		grant := grants[id]
		expected, _ := json.Marshal(houseTanks.SnapshotForConnection(driver.frame, id, "connection-"+id, &grant))
		if string(rendered) != string(expected) || view.ViewKey(viewerOf(id)) == view.ViewKey(module.Viewer{PlayerID: "outsider"}) {
			t.Fatalf("%s view differs from its projection", id)
		}
	}
	spectator, _ := render(driver.frame, module.Viewer{PlayerID: "outsider", ConnectionID: "x"})
	var shared houseTanks.HouseTanksSnapshotModel
	_ = json.Unmarshal(spectator, &shared)
	for _, player := range shared.Players {
		if player.ControlGeneration != nil {
			t.Fatal("a spectator sees no generation")
		}
	}
	// Between rounds a destroyed tank's player still sees its generation, so
	// it can vote.
	driver.destroyAllBut("a")
	driver.ticks(1)
	rendered, _ := render(driver.frame, viewerOf("b"))
	var between houseTanks.HouseTanksSnapshotModel
	_ = json.Unmarshal(rendered, &between)
	if between.Players[1].ControlGeneration == nil || *between.Players[1].ControlGeneration != grants["b"].ControlGeneration || between.Players[1].IsAlive {
		t.Fatal("the loser keeps its controls for the vote")
	}
	// The wire shapes are the fixture's.
	fixture := readHouseTanksFixture[houseTanksProtocolFixture](t, "houseTanksProtocol")
	example := func(name string) map[string]any {
		for _, entry := range fixture.ServerMessages {
			if entry.Name == name {
				var message struct {
					Payload map[string]any `json:"payload"`
				}
				_ = json.Unmarshal(entry.Message, &message)
				return message.Payload
			}
		}
		t.Fatalf("fixture has no %s", name)
		return nil
	}
	var actual map[string]any
	_ = json.Unmarshal(rendered, &actual)
	sameKeys := func(label string, expected, actual any) {
		t.Helper()
		keys := func(value any) []string {
			object, _ := value.(map[string]any)
			names := make([]string, 0, len(object))
			for name := range object {
				names = append(names, name)
			}
			sort.Strings(names)
			return names
		}
		if !reflect.DeepEqual(keys(expected), keys(actual)) {
			t.Fatalf("%s keys = %v, want %v", label, keys(actual), keys(expected))
		}
	}
	roundReady := example("roundReady")
	sameKeys("snapshot", roundReady, actual)
	sameKeys("player", roundReady["players"].([]any)[0], actual["players"].([]any)[0])
	sameKeys("tank", roundReady["tanks"].([]any)[0], actual["tanks"].([]any)[0])
	sameKeys("crate", roundReady["crates"].([]any)[0], actual["crates"].([]any)[0])
	if actual["roundPhase"] != "roundReady" || actual["seed"] != houseTanks.SeedText(houseTanks.MatchSeed("contract-session")) {
		t.Fatal("round phase and seed")
	}
	// The lobby preview already shows the match's map.
	preview, err := game.PrepareFrame(mustPreview(t, game))
	if err != nil {
		t.Fatal(err)
	}
	previewed, _ := preview.Render(module.Viewer{PlayerID: "a"})
	var previewModel map[string]any
	_ = json.Unmarshal(previewed, &previewModel)
	sameKeys("preview", example("countdown"), previewModel)
	if previewModel["roundPhase"] != nil || previewModel["roundNumber"] != 0.0 || previewModel["countdownEndsAt"] == nil || previewModel["mapId"] != string(driver.match().MapID) {
		t.Fatalf("preview = %v %v %v", previewModel["roundPhase"], previewModel["roundNumber"], previewModel["mapId"])
	}
}

func mustPreview(t *testing.T, game module.TickModule) json.RawMessage {
	t.Helper()
	frame, err := game.PreviewFrame(module.PreviewParams{SessionID: "contract-session", HouseID: "contract-house", CountdownEndsAt: time.Now().Add(3 * time.Second),
		Players: []module.Player{{PlayerID: "a", DisplayName: "A"}, {PlayerID: "b", DisplayName: "B"}, {PlayerID: "c", DisplayName: "C"}}, Epoch: 1, Sequence: 2})
	if err != nil {
		t.Fatal(err)
	}
	return frame.Payload
}
