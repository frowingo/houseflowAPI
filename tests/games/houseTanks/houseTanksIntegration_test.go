package houseTanks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games/houseTanks"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit/gamekit"
)

// persistTanksSession stores a running House Tanks session of the house owner
// and member that started at startedAt.
func persistTanksSession(t *testing.T, fixture *gamekit.GameSessionApplicationFixture, startedAt time.Time) gameDomain.SessionSnapshot {
	t.Helper()
	rules := houseTanks.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 0, 0
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "tanks-runtime-session", HouseID: fixture.HouseID, GameKey: houseTanks.GameKey,
		ProtocolVersion: houseTanks.ProtocolVersion, Mode: gameDomain.RealtimeGame, Rules: rules, CreatedBy: fixture.OwnerID, CreatedAt: startedAt})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fixture.OwnerID, fixture.MemberID} {
		if err := session.JoinPlayer(id, startedAt); err != nil {
			t.Fatal(err)
		}
		if err := session.SetReady(id, true, startedAt); err != nil {
			t.Fatal(err)
		}
	}
	if session.Snapshot().State != gameDomain.SessionRunning {
		t.Fatal("expected running session")
	}
	if _, err := fixture.Repository.Create(fixture.Ctx, session.Snapshot(), session.PendingEvents(), gamekit.PersistenceCommand("tanks-create", "create", "tanks")); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.Repository.FindByID(fixture.Ctx, session.Snapshot().SessionID)
	if err != nil {
		t.Fatal(err)
	}
	return stored.Snapshot()
}

func newTanksGatewayFixture(t *testing.T, rules gameDomain.SessionRules) *gamekit.RocketsGatewayFixture {
	t.Helper()
	definition := houseTanks.Definition()
	definition.Rules = rules
	return gamekit.NewGameGatewayFixture(t, definition, 0, 0)
}

func roundPhaseIs(model houseTanks.HouseTanksSnapshotModel, phase houseTanks.RoundPhase) bool {
	return model.Phase == houseTanks.PhasePlaying && model.RoundPhase != nil && *model.RoundPhase == phase
}

func firedBy(model houseTanks.HouseTanksSnapshotModel, playerID string) bool {
	for _, event := range model.Events {
		if event.Kind == houseTanks.EventFired && event.PlayerID != nil && *event.PlayerID == playerID {
			return true
		}
	}
	return false
}

func TestHouseTanksGatewayTwoInstancesRoundFlowAndInsufficientPlayers(t *testing.T) {
	rules := houseTanks.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 300*time.Millisecond, 600*time.Millisecond
	fixture := newTanksGatewayFixture(t, rules)
	session := fixture.EnsureHTTP(t)
	first := fixture.Connect(t, 0, session.SessionId, fixture.OwnerID)
	second := fixture.Connect(t, 1, session.SessionId, fixture.MemberID)
	first.KeepAlive(t)
	second.KeepAlive(t)
	first.Lifecycle(t, gameCommands.JoinGameSessionCommandType, "join-first", struct{}{})
	second.Lifecycle(t, gameCommands.JoinGameSessionCommandType, "join-second", struct{}{})
	first.Lifecycle(t, gameCommands.SetPlayerReadyCommandType, "ready-first", dtos.RealtimeReadyModel{Ready: true})
	second.Lifecycle(t, gameCommands.SetPlayerReadyCommandType, "ready-second", dtos.RealtimeReadyModel{Ready: true})
	// The lobby countdown already shows the arena of the match.
	countdown := first.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool { return model.Phase == houseTanks.PhaseCountdown })
	if countdown.CountdownEndsAt == nil || countdown.RoundPhase != nil || countdown.RoundNumber != 0 || countdown.MapID == "" || len(countdown.Tanks) != 2 || countdown.Players[0].DisplayName != "Player0 House" {
		t.Fatalf("countdown: %+v", countdown)
	}
	grant1, grant2 := first.TanksGrant(t, 0), second.TanksGrant(t, 0)
	if grant1.RuntimeEpoch != grant2.RuntimeEpoch || grant1.ControlGeneration == grant2.ControlGeneration {
		t.Fatalf("grants: %+v %+v", grant1, grant2)
	}
	counting := first.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool {
		return roundPhaseIs(model, houseTanks.RoundCountdown) && model.Players[0].ControlGeneration != nil
	})
	if counting.RoundNumber != 1 || counting.MapID != countdown.MapID || counting.Players[1].ControlGeneration != nil || counting.RoundCountdownEndsAt == nil {
		t.Fatalf("round countdown: %+v", counting)
	}
	// A vote outside the ready window is refused on the remote instance too.
	second.Send(t, houseTanks.RoundReadyMessageType, "early-vote", houseTanks.HouseTanksRoundReadyModel{ControlGeneration: grant2.ControlGeneration, Ready: true})
	if rejection := second.Rejection(t, "early-vote"); rejection.Error == nil || rejection.Error.Code != houseTanks.InvalidInputErrorCode {
		t.Fatalf("early vote: %+v", rejection)
	}
	playing := first.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool {
		return roundPhaseIs(model, houseTanks.RoundPlaying)
	})
	// Both presses reach the owner, whichever instance holds the socket.
	first.Send(t, houseTanks.PressMessageType, "press-first", houseTanks.HouseTanksPressModel{ControlGeneration: grant1.ControlGeneration, InputSequence: 1, Pressed: true, ClientTick: playing.Tick, Heading: playing.Tanks[0].Rotation})
	second.Send(t, houseTanks.PressMessageType, "press-second", houseTanks.HouseTanksPressModel{ControlGeneration: grant2.ControlGeneration, InputSequence: 1, Pressed: true, ClientTick: playing.Tick, Heading: playing.Tanks[1].Rotation})
	driving := second.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool {
		return model.Players[0].LastProcessedInputSequence == 1 && model.Players[1].LastProcessedInputSequence == 1
	})
	if !driving.Tanks[0].IsDriving || !driving.Tanks[1].IsDriving || !firedBy(driving, fixture.OwnerID) || !firedBy(driving, fixture.MemberID) ||
		driving.Players[0].ControlGeneration != nil || driving.Players[1].ControlGeneration == nil {
		t.Fatalf("remote press/ACK/privacy: %+v", driving)
	}
	first.Send(t, houseTanks.PressMessageType, "release-first", houseTanks.HouseTanksPressModel{ControlGeneration: grant1.ControlGeneration, InputSequence: 2, Pressed: false, ClientTick: driving.Tick, Heading: 0})
	first.Send(t, houseTanks.PressMessageType, "stale-first", houseTanks.HouseTanksPressModel{ControlGeneration: grant1.ControlGeneration, InputSequence: 2, Pressed: true, ClientTick: driving.Tick, Heading: 0})
	if rejection := first.Rejection(t, "stale-first"); rejection.Error == nil || rejection.Error.Code != houseTanks.StaleControlErrorCode {
		t.Fatalf("stale press: %+v", rejection)
	}
	second.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool {
		return model.Players[0].LastProcessedInputSequence == 2 && !model.Tanks[0].IsDriving
	})
	second.Send(t, houseTanks.ResyncMessageType, "resync", struct{}{})
	second.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == houseTanks.SnapshotMessageType && message.MessageID == "resync"
	})
	// One player left: the match cannot go on and is cancelled, keeping its map.
	first.Lifecycle(t, gameCommands.LeaveGameSessionCommandType, "leave", struct{}{})
	final := second.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool { return model.Phase == houseTanks.PhaseFinalizing })
	if !final.Players[0].HasLeft || final.ChampionID != nil || *final.RoundPhase != houseTanks.MatchEnded {
		t.Fatalf("finalizing: %+v", final)
	}
	// A cancelled result commits into the cancelled phase, not ended.
	second.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool { return model.Phase == houseTanks.PhaseCancelled })
	resultMessage := second.Await(t, func(message realtime.ServerMessage) bool { return message.Type == houseTanks.ResultMessageType })
	var result houseTanks.HouseTanksResultModel
	if err := json.Unmarshal(resultMessage.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != houseTanks.ResultCancelled || result.EndReason != houseTanks.EndInsufficientPlayers || result.ChampionID != nil || result.MapID == nil ||
		*result.MapID != countdown.MapID || result.Players[0].FinalRank != nil {
		t.Fatalf("result: %+v", result)
	}
	stored, err := fixture.Repository.FindByID(fixture.Ctx, session.SessionId)
	if err != nil || stored.Snapshot().State != gameDomain.SessionCancelled {
		t.Fatalf("completion not persisted: %v %s", err, stored.Snapshot().State)
	}
	for index := range fixture.URLs {
		status, read := fixture.TanksResultHTTP(t, index, session.SessionId, fixture.MemberID)
		if status != 200 || !bytes.Equal(resultMessage.Payload, read) {
			t.Fatalf("HTTP/socket canonical mismatch: %d %s", status, read)
		}
	}
	if status, _ := fixture.TanksResultHTTP(t, 0, session.SessionId, fixture.OutsiderID); status != 403 {
		t.Fatalf("outsider result status: %d", status)
	}
	rematch := fixture.EnsureHTTP(t)
	if rematch.SessionId == session.SessionId || rematch.State != string(gameDomain.SessionLobby) || len(rematch.Players) != 0 {
		t.Fatalf("rematch reset old game: %+v", rematch)
	}
}

// saveTanksCheckpoint plays a match on a crashed owner's runtime and leaves
// its checkpoint behind, as an owner that died after saving it would.
func saveTanksCheckpoint(t *testing.T, fixture *gamekit.GameSessionApplicationFixture, snapshot gameDomain.SessionSnapshot, play func(driver *tanksDriver)) houseTanks.RuntimeCheckpoint {
	t.Helper()
	coordinator := gamekit.NewTestCoordinator(t, os.Getenv("HOUSEFLOW_TEST_REDIS_URL"), "tanks-crashed-owner")
	lease, acquired, err := coordinator.AcquireRoom(fixture.Ctx, snapshot.SessionID, 10*time.Second)
	if err != nil || !acquired {
		t.Fatal(err)
	}
	owners := fixture.Repository.(gameAbstract.RuntimeOwnerRepository)
	owner, err := owners.ClaimRuntimeOwner(fixture.Ctx, 0, gameAbstract.RuntimeOwner{SessionID: snapshot.SessionID, OwnerInstanceID: lease.OwnerInstanceID, LeaseID: lease.LeaseID})
	if err != nil {
		t.Fatal(err)
	}
	if err := owners.MarkRuntimeStarted(fixture.Ctx, owner); err != nil {
		t.Fatal(err)
	}
	session := houseTanks.RuntimeSession{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, StartedAt: snapshot.StartedAt,
		Players: []houseTanks.RuntimePlayer{{PlayerID: fixture.OwnerID, DisplayName: "Owner"}, {PlayerID: fixture.MemberID, DisplayName: "Member"}}}
	runtime, err := houseTanks.NewRuntime(session, owner.Generation, snapshot.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	driver := attachTanksDriver(t, runtime, owner.Generation, snapshot.StartedAt)
	play(driver)
	checkpoint := runtime.Checkpoint()
	payload, _ := json.Marshal(checkpoint)
	if err := coordinator.SaveRuntimeCheckpoint(fixture.Ctx, lease, coordinationAbstract.RuntimeCheckpoint{Epoch: checkpoint.Epoch, Sequence: checkpoint.Sequence, CapturedAt: checkpoint.CapturedAt, Payload: payload}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ReleaseRoom(fixture.Ctx, lease); err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

// An owner lost between rounds: the new owner resumes the ready window with
// the score, every player rebinds — the loser too, to vote — and the votes
// start the next round.
func TestHouseTanksRecoveryResumesRoundReadyAcrossInstances(t *testing.T) {
	fixture := newTanksGatewayFixture(t, houseTanks.Definition().Rules)
	snapshot := persistTanksSession(t, fixture.GameSessionApplicationFixture, time.Now().UTC().Add(-8*time.Second))
	var lostTick int64
	checkpoint := saveTanksCheckpoint(t, fixture.GameSessionApplicationFixture, snapshot, func(driver *tanksDriver) {
		driver.bind(fixture.OwnerID, "crashed-owner-connection")
		driver.bind(fixture.MemberID, "crashed-member-connection")
		driver.untilRoundPhase(houseTanks.RoundPlaying)
		driver.destroyAllBut(fixture.OwnerID)
		driver.ticks(1)
		if err := driver.ready(fixture.OwnerID, true); err != nil {
			t.Fatal(err)
		}
		driver.ticks(1)
		lostTick = driver.match().Tick
	})
	first := fixture.Connect(t, 0, snapshot.SessionID, fixture.OwnerID)
	second := fixture.Connect(t, 1, snapshot.SessionID, fixture.MemberID)
	first.KeepAlive(t)
	second.KeepAlive(t)
	grant1, grant2 := first.TanksGrant(t, checkpoint.Epoch), second.TanksGrant(t, checkpoint.Epoch)
	resumed := second.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool {
		return model.RuntimeEpoch == grant2.RuntimeEpoch && model.Players[1].ControlGeneration != nil
	})
	if !roundPhaseIs(resumed, houseTanks.RoundReady) || resumed.RoundNumber != 1 || resumed.Players[0].RoundsWon != 1 || resumed.Players[1].IsAlive ||
		resumed.Players[0].RoundReady || resumed.Tick < lostTick || resumed.Tick > lostTick+houseTanks.PhysicsRateHz*3 {
		t.Fatalf("resumed: tick %d (lost %d) %+v", resumed.Tick, lostTick, resumed)
	}
	if *resumed.Players[1].ControlGeneration != grant2.ControlGeneration || grant1.RuntimeEpoch != grant2.RuntimeEpoch {
		t.Fatal("the loser must hold the controls to vote")
	}
	first.Send(t, houseTanks.RoundReadyMessageType, "vote-first", houseTanks.HouseTanksRoundReadyModel{ControlGeneration: grant1.ControlGeneration, Ready: true})
	second.Send(t, houseTanks.RoundReadyMessageType, "vote-second", houseTanks.HouseTanksRoundReadyModel{ControlGeneration: grant2.ControlGeneration, Ready: true})
	next := first.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool {
		return roundPhaseIs(model, houseTanks.RoundCountdown)
	})
	if next.RoundNumber != 2 || !next.Players[1].IsAlive || next.Players[0].RoundsWon != 1 {
		t.Fatalf("next round: %+v", next)
	}
}

// A real child owner is killed in the middle of a round. The same sockets
// recover the round where it was, with stationary tanks until each rebinds.
func TestHouseTanksOwnerProcessKillRecoversMidRound(t *testing.T) {
	fixture := newTanksGatewayFixture(t, houseTanks.Definition().Rules)
	snapshot := persistTanksSession(t, fixture.GameSessionApplicationFixture, time.Now().UTC())
	command := exec.Command(os.Args[0], "-test.run=^TestHouseRocketsOwnerProcessHelper$")
	command.Env = append(os.Environ(), "HOUSEFLOW_RECOVERY_HELPER=true", "HOUSEFLOW_RECOVERY_DB="+fixture.DB.Name(), "HOUSEFLOW_RECOVERY_SESSION="+snapshot.SessionID)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { _ = command.Process.Kill(); _ = command.Wait() }) }
	t.Cleanup(stop)
	ready := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "ownerReady" {
				ready <- struct{}{}
				return
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		stop()
		t.Fatalf("child owner did not start: %s", stderr.String())
	}
	first := fixture.Connect(t, 0, snapshot.SessionID, fixture.OwnerID)
	second := fixture.Connect(t, 1, snapshot.SessionID, fixture.MemberID)
	first.KeepAlive(t)
	second.KeepAlive(t)
	grant := first.TanksGrant(t, 0)
	second.TanksGrant(t, 0)
	playing := first.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool {
		return roundPhaseIs(model, houseTanks.RoundPlaying)
	})
	first.Send(t, houseTanks.PressMessageType, "drive", houseTanks.HouseTanksPressModel{ControlGeneration: grant.ControlGeneration, InputSequence: 1, Pressed: true, ClientTick: playing.Tick, Heading: playing.Tanks[0].Rotation})
	before := first.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool {
		return model.Players[0].LastProcessedInputSequence == 1 && model.Tanks[0].IsDriving
	})
	stop()
	newGrant := first.TanksGrant(t, grant.RuntimeEpoch)
	after := first.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool {
		return model.RuntimeEpoch == newGrant.RuntimeEpoch && model.Phase == houseTanks.PhasePlaying
	})
	// The checkpoint interval is 50 ms; no ticks are spent on the ownership gap.
	if !roundPhaseIs(after, houseTanks.RoundPlaying) || after.RoundNumber != before.RoundNumber || after.Tick > before.Tick+houseTanks.PhysicsRateHz/2 ||
		after.Tanks[0].IsDriving || len(after.Players) != len(before.Players) {
		t.Fatalf("recovery: before=%+v after=%+v", before, after)
	}
	if newGrant.ControlGeneration == grant.ControlGeneration {
		t.Fatal("old control survived process kill")
	}
	first.Send(t, houseTanks.PressMessageType, "old-control", houseTanks.HouseTanksPressModel{ControlGeneration: grant.ControlGeneration, InputSequence: 2, Pressed: true, ClientTick: after.Tick, Heading: 0})
	if rejection := first.Rejection(t, "old-control"); rejection.Error == nil || rejection.Error.Code != houseTanks.StaleControlErrorCode {
		t.Fatalf("old generation: %+v", rejection)
	}
	first.Send(t, houseTanks.PressMessageType, "post-recovery", houseTanks.HouseTanksPressModel{ControlGeneration: newGrant.ControlGeneration, InputSequence: 1, Pressed: true, ClientTick: after.Tick, Heading: after.Tanks[0].Rotation})
	first.TanksSnapshot(t, func(model houseTanks.HouseTanksSnapshotModel) bool {
		return model.RuntimeEpoch == newGrant.RuntimeEpoch && model.Players[0].LastProcessedInputSequence == 1 && model.Tanks[0].IsDriving
	})
}

// A decided champion commits once through the result transaction, however
// many owners race to complete it, and never overwrites a rematch.
func TestHouseTanksResultChampionCommitsOnceAndRematchSafe(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	snapshot := persistTanksSession(t, fixture, time.Now().UTC().Add(-time.Minute))
	owners := fixture.Repository.(gameAbstract.RuntimeOwnerRepository)
	owner, err := owners.ClaimRuntimeOwner(fixture.Ctx, 0, gameAbstract.RuntimeOwner{SessionID: snapshot.SessionID, OwnerInstanceID: "result-owner", LeaseID: "result-lease"})
	if err != nil {
		t.Fatal(err)
	}
	if err := owners.MarkRuntimeStarted(fixture.Ctx, owner); err != nil {
		t.Fatal(err)
	}
	session := houseTanks.RuntimeSession{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, StartedAt: snapshot.StartedAt,
		Players: []houseTanks.RuntimePlayer{{PlayerID: fixture.OwnerID, DisplayName: "Owner"}, {PlayerID: fixture.MemberID, DisplayName: "Member"}}}
	runtime, err := houseTanks.NewRuntime(session, owner.Generation, snapshot.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	driver := attachTanksDriver(t, runtime, owner.Generation, snapshot.StartedAt)
	driver.bind(fixture.OwnerID, "owner-connection")
	driver.bind(fixture.MemberID, "member-connection")
	driver.playWinners(fixture.MemberID, fixture.OwnerID, fixture.MemberID, fixture.MemberID)
	proposed := driver.onlyResult()
	if proposed.Status != houseTanks.ResultCompleted || *proposed.ChampionID != fixture.MemberID || *proposed.Players[1].FinalRank != 1 || *proposed.Players[0].FinalRank != 2 {
		t.Fatalf("proposed: %+v", proposed)
	}
	encoded, err := houseTanks.EncodeMatchResult(proposed)
	if err != nil {
		t.Fatal(err)
	}
	matches := database.NewGameMatchRepository(fixture.DB.Client(), fixture.DB.Name())
	handler := gameCommands.NewCompleteMatchHandler(fixture.Repository, matches, gamekit.RocketsGames())
	completion := gameCommands.CompleteMatchCommand{Result: encoded, Owner: owner}
	start := make(chan struct{})
	var workers sync.WaitGroup
	committed := make(chan gameDomain.MatchResult, 4)
	failures := make(chan error, 4)
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			stored, err := handler.Handle(fixture.Ctx, completion)
			failures <- err
			committed <- stored
		}()
	}
	close(start)
	workers.Wait()
	close(failures)
	close(committed)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var canonical []byte
	for stored := range committed {
		if canonical == nil {
			canonical = stored.Payload
		}
		if !bytes.Equal(stored.Payload, canonical) {
			t.Fatal("canonical result changed between completions")
		}
	}
	read, err := matches.FindResult(fixture.Ctx, snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := houseTanks.DecodeMatchResult(read)
	if err != nil || !bytes.Equal(read.Payload, canonical) || result.ChampionID == nil || *result.ChampionID != fixture.MemberID || result.RoundsPlayed != 4 {
		t.Fatalf("stored: %+v %v", result, err)
	}
	stored, err := fixture.Repository.FindByID(fixture.Ctx, snapshot.SessionID)
	if err != nil || stored.Snapshot().State != gameDomain.SessionFinished {
		t.Fatalf("terminal session: %v", err)
	}
	if _, err := fixture.Repository.FindActive(fixture.Ctx, fixture.HouseID, houseTanks.GameKey); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("active slot retained: %v", err)
	}
	// A rematch is a new session in the same slot; replaying the old
	// completion neither fails nor touches it.
	rematch, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "tanks-rematch-session", HouseID: fixture.HouseID, GameKey: houseTanks.GameKey, ProtocolVersion: houseTanks.ProtocolVersion,
		Mode: gameDomain.RealtimeGame, Rules: houseTanks.Definition().Rules, CreatedBy: fixture.OwnerID, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Repository.Create(fixture.Ctx, rematch.Snapshot(), rematch.PendingEvents(), gamekit.PersistenceCommand("tanks-rematch-create", "create", "rematch")); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Handle(fixture.Ctx, completion); err != nil {
		t.Fatal(err)
	}
	active, err := fixture.Repository.FindActive(fixture.Ctx, fixture.HouseID, houseTanks.GameKey)
	if err != nil || active.Snapshot().SessionID != "tanks-rematch-session" {
		t.Fatalf("old completion deleted the rematch: %v", err)
	}
	different := proposed
	different.EndedAt = different.EndedAt.Add(time.Second)
	changed, err := houseTanks.EncodeMatchResult(different)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Handle(fixture.Ctx, gameCommands.CompleteMatchCommand{Result: changed, Owner: owner}); err == nil {
		t.Fatal("a different result overwrote the canonical one")
	}
}
