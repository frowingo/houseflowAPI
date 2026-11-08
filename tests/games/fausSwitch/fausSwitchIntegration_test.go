package fausSwitch

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games/fausSwitch"
	"houseflowApi/internal/models/dtos"
	"houseflowApi/tests/testkit/gamekit"
)

// standing is a robot running on a rail: a flip at this tick turns it.
func standing(runner fausSwitch.FausSwitchRunnerModel) bool {
	return runner.Status == fausSwitch.RunnerRunning && runner.VelocityY == 0
}

func switchColors(snapshot fausSwitch.FausSwitchSnapshotModel) map[string]fausSwitch.Color {
	colors := map[string]fausSwitch.Color{}
	for _, runner := range snapshot.Runners {
		colors[runner.PlayerID] = runner.Color
	}
	return colors
}

func sameColors(got, want map[string]fausSwitch.Color) bool {
	if len(got) != len(want) {
		return false
	}
	for id, color := range want {
		if got[id] != color {
			return false
		}
	}
	return true
}

// The whole online flow on two gateway instances: every player claims a
// color in the lobby, the countdown shows the race in those colors, every
// robot is flipped once by its own controller and the race ends by itself
// with one canonical result. Two players wait out the ready window; a full
// room of eight starts as soon as the last player is ready.
func TestFausSwitchGatewayRaceFlow(t *testing.T) {
	for _, count := range []int{2, fausSwitch.MaximumPlayers} {
		t.Run(fmt.Sprintf("%d players", count), func(t *testing.T) { playSwitchRace(t, count) })
	}
}

func playSwitchRace(t *testing.T, count int) {
	definition := fausSwitch.Definition()
	definition.Rules.CountdownDuration = 600 * time.Millisecond
	if count < fausSwitch.MaximumPlayers {
		definition.Rules.ReadyWindowDuration = 300 * time.Millisecond
	}
	fixture := gamekit.NewGameGatewayFixture(t, definition, 0, 0)
	players := fixture.HousePlayers(t, count)
	session := fixture.EnsureHTTP(t)
	sockets := make([]*gamekit.RocketsSocket, count)
	for seat, id := range players {
		sockets[seat] = fixture.Connect(t, seat%2, session.SessionId, id)
		sockets[seat].KeepAlive(t)
		sockets[seat].Lifecycle(t, gameCommands.JoinGameSessionCommandType, fmt.Sprintf("join-%d", seat), struct{}{})
	}
	// Seat s claims the catalog's colors from the end. The last player first
	// asks for the first player's color: the claim is accepted and changes
	// nothing.
	catalog := fausSwitch.PlayerColors()
	want := map[string]fausSwitch.Color{}
	claim := func(seat int, color fausSwitch.Color) {
		t.Helper()
		messageID := fmt.Sprintf("color-%d-%s", seat, color)
		sockets[seat].Send(t, fausSwitch.SelectColorMessageType, messageID, fausSwitch.FausSwitchSelectColorModel{Color: color})
		sockets[seat].Accepted(t, messageID)
	}
	claim(0, catalog[len(catalog)-1])
	want[players[0]] = catalog[len(catalog)-1]
	sockets[count-1].SessionColors(t, want)
	claim(count-1, catalog[len(catalog)-1])
	for seat := 1; seat < count; seat++ {
		claim(seat, catalog[len(catalog)-1-seat])
		want[players[seat]] = catalog[len(catalog)-1-seat]
	}
	for _, socket := range sockets {
		socket.SessionColors(t, want)
	}
	for seat, socket := range sockets {
		socket.Lifecycle(t, gameCommands.SetPlayerReadyCommandType, fmt.Sprintf("ready-%d", seat), dtos.RealtimeReadyModel{Ready: true})
	}
	// The countdown already shows the race: every robot on its start
	// platform in its claimed color, at the race's scale.
	scale := 1.0
	if count > fausSwitch.CrowdedRunnerThreshold {
		scale = fausSwitch.CrowdedRunnerScale
	}
	for _, socket := range sockets {
		countdown := socket.SwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool { return model.Phase == fausSwitch.PhaseCountdown })
		if countdown.Tick != 0 || countdown.CountdownEndsAt == nil || countdown.RunnerScale != scale || countdown.CourseVersion != fausSwitch.CourseVersion ||
			len(countdown.Runners) != count || !sameColors(switchColors(countdown), want) || countdown.Runners[0].DisplayName != "Player0 House" {
			t.Fatalf("countdown: %+v", countdown)
		}
	}
	grants := make([]fausSwitch.FausSwitchControlGrantedModel, count)
	for seat, socket := range sockets {
		grants[seat] = socket.SwitchGrant(t, 0)
		if grants[seat].PlayerID != players[seat] || grants[seat].RuntimeEpoch != grants[0].RuntimeEpoch {
			t.Fatalf("grant %d: %+v", seat, grants[seat])
		}
	}
	// Each player flips its own robot once while it runs on a rail; the flip
	// is applied at the tick on the player's screen.
	turned := make([]fausSwitch.Gravity, count)
	for seat, socket := range sockets {
		seen := socket.LiveSwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
			return model.Phase == fausSwitch.PhasePlaying && model.Tick >= fausSwitch.PhysicsRateHz && standing(model.Runners[seat])
		})
		turned[seat] = fausSwitch.GravityUp + fausSwitch.GravityDown - seen.Runners[seat].Gravity
		socket.Flip(t, fmt.Sprintf("flip-%d", seat), grants[seat].ControlGeneration, 1, seen.Tick)
	}
	for seat, socket := range sockets {
		acknowledged := socket.SwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
			return model.Runners[seat].LastProcessedInputSequence == 1
		})
		if acknowledged.Runners[seat].Gravity != turned[seat] {
			t.Fatalf("seat %d flip was not applied: %+v", seat, acknowledged.Runners[seat])
		}
		// Only a player's own robot carries its generation.
		for other, runner := range acknowledged.Runners {
			if (runner.ControlGeneration != nil) != (other == seat) || !runner.Connected {
				t.Fatalf("seat %d sees runner %d: %+v", seat, other, runner)
			}
		}
		if *acknowledged.Runners[seat].ControlGeneration != grants[seat].ControlGeneration {
			t.Fatalf("seat %d generation", seat)
		}
	}
	// The committed race is announced before its result.
	ended := sockets[0].SwitchSnapshotWithin(t, gamekit.SwitchRaceTimeout, func(model fausSwitch.FausSwitchSnapshotModel) bool { return model.Phase == fausSwitch.PhaseEnded })
	results := make([]fausSwitch.FausSwitchResultModel, count)
	for seat, socket := range sockets {
		results[seat] = fixture.SwitchResult(t, socket, session.SessionId, players[seat])
	}
	result := results[0]
	if result.Status != fausSwitch.ResultCompleted || result.EndReason != fausSwitch.EndRaceComplete || result.StartedAt == nil ||
		result.CourseVersion != fausSwitch.CourseVersion || len(result.Players) != count {
		t.Fatalf("result: %+v", result)
	}
	for seat, player := range result.Players {
		if player.PlayerID != players[seat] || player.Rank == nil || *player.Rank < 1 || *player.Rank > count {
			t.Fatalf("standing %d: %+v", seat, player)
		}
	}
	if (ended.WinnerID == nil) != (result.WinnerID == nil) || (ended.WinnerID != nil && *ended.WinnerID != *result.WinnerID) {
		t.Fatalf("ended snapshot winner %v, result %v", ended.WinnerID, result.WinnerID)
	}
	stored, err := fixture.Repository.FindByID(fixture.Ctx, session.SessionId)
	if err != nil || stored.Snapshot().State != gameDomain.SessionFinished || !sameColors(gamekit.ColorsOf(t, stored.Snapshot().LobbyState), want) {
		t.Fatalf("stored session: %v %+v", err, stored.Snapshot())
	}
	if status, _ := fixture.TanksResultHTTP(t, 0, session.SessionId, fixture.OutsiderID); status != 403 {
		t.Fatalf("outsider result status: %d", status)
	}
}

// persistSwitchSession stores a running two-player race of the house owner
// and member, with their lobby colors, that started at startedAt.
func persistSwitchSession(t *testing.T, fixture *gamekit.GameSessionApplicationFixture, startedAt time.Time, colors map[string]fausSwitch.Color) gameDomain.SessionSnapshot {
	t.Helper()
	rules := fausSwitch.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 0, 0
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "switch-runtime-session", HouseID: fixture.HouseID, GameKey: fausSwitch.GameKey,
		ProtocolVersion: fausSwitch.ProtocolVersion, Mode: gameDomain.RealtimeGame, Rules: rules, CreatedBy: fixture.OwnerID, CreatedAt: startedAt})
	if err != nil {
		t.Fatal(err)
	}
	players := []string{fixture.OwnerID, fixture.MemberID}
	for _, id := range players {
		if err := session.JoinPlayer(id, startedAt); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range players {
		intent, _ := json.Marshal(fausSwitch.FausSwitchSelectColorModel{Color: colors[id]})
		state, err := fausSwitch.ApplyLobbyIntent(session.Snapshot(), id, fausSwitch.SelectColorMessageType, intent)
		if err != nil {
			t.Fatal(err)
		}
		if err := session.UpdateLobbyState(id, state, startedAt); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range players {
		if err := session.SetReady(id, true, startedAt); err != nil {
			t.Fatal(err)
		}
	}
	if session.Snapshot().State != gameDomain.SessionRunning {
		t.Fatal("expected running session")
	}
	if _, err := fixture.Repository.Create(fixture.Ctx, session.Snapshot(), session.PendingEvents(), gamekit.PersistenceCommand("switch-create", "create", "switch")); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.Repository.FindByID(fixture.Ctx, session.Snapshot().SessionID)
	if err != nil {
		t.Fatal(err)
	}
	return stored.Snapshot()
}

// startOwnerProcess runs the session's owner in a child process, which the
// returned function kills.
func startOwnerProcess(t *testing.T, fixture *gamekit.RocketsGatewayFixture, sessionID string) func() {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestHouseRocketsOwnerProcessHelper$")
	command.Env = append(os.Environ(), "HOUSEFLOW_RECOVERY_HELPER=true", "HOUSEFLOW_RECOVERY_DB="+fixture.DB.Name(), "HOUSEFLOW_RECOVERY_SESSION="+sessionID)
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
	return stop
}

// A real child owner is killed in the middle of the race. The same sockets
// recover it where the last checkpoint left it — robots, colors and the flip
// already applied — under new controls, and the race ends with one result.
func TestFausSwitchOwnerProcessKillRecoversMidRace(t *testing.T) {
	fixture := gamekit.NewGameGatewayFixture(t, fausSwitch.Definition(), 0, 0)
	colors := map[string]fausSwitch.Color{fixture.OwnerID: fausSwitch.ColorBerry, fixture.MemberID: fausSwitch.ColorOcean}
	snapshot := persistSwitchSession(t, fixture.GameSessionApplicationFixture, time.Now().UTC(), colors)
	stop := startOwnerProcess(t, fixture, snapshot.SessionID)
	first := fixture.Connect(t, 0, snapshot.SessionID, fixture.OwnerID)
	second := fixture.Connect(t, 1, snapshot.SessionID, fixture.MemberID)
	first.KeepAlive(t)
	second.KeepAlive(t)
	grant := first.SwitchGrant(t, 0)
	second.SwitchGrant(t, 0)
	seen := first.LiveSwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
		return model.Phase == fausSwitch.PhasePlaying && model.Tick >= fausSwitch.PhysicsRateHz && standing(model.Runners[0])
	})
	first.Flip(t, "flip", grant.ControlGeneration, 1, seen.Tick)
	flipped := first.SwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
		return model.Runners[0].LastProcessedInputSequence == 1
	})
	if flipped.Runners[0].Gravity == seen.Runners[0].Gravity {
		t.Fatalf("flip not applied: %+v", flipped.Runners[0])
	}
	// The checkpoint interval is 50 ms: let one pass after the flip.
	before := first.SwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
		return model.Tick >= flipped.Tick+fausSwitch.PhysicsRateHz/5
	})
	stop()
	newGrant := first.SwitchGrant(t, grant.RuntimeEpoch)
	second.SwitchGrant(t, grant.RuntimeEpoch)
	after := first.SwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
		return model.RuntimeEpoch == newGrant.RuntimeEpoch && model.Phase == fausSwitch.PhasePlaying && model.Runners[0].ControlGeneration != nil
	})
	// No ticks are spent on the ownership gap.
	if after.Tick < before.Tick || after.Tick > before.Tick+fausSwitch.PhysicsRateHz || !sameColors(switchColors(after), colors) ||
		after.Runners[0].Gravity != flipped.Runners[0].Gravity || *after.Runners[0].ControlGeneration != newGrant.ControlGeneration {
		t.Fatalf("recovery: before=%+v after=%+v", before, after)
	}
	if newGrant.ControlGeneration == grant.ControlGeneration {
		t.Fatal("old control survived process kill")
	}
	first.Flip(t, "old-control", grant.ControlGeneration, 2, after.Tick)
	if rejection := first.Rejection(t, "old-control"); rejection.Error == nil || rejection.Error.Code != fausSwitch.StaleControlErrorCode {
		t.Fatalf("old generation: %+v", rejection)
	}
	// The new control flips the robot back.
	again := first.LiveSwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
		return model.RuntimeEpoch == newGrant.RuntimeEpoch && standing(model.Runners[0])
	})
	first.Flip(t, "post-recovery", newGrant.ControlGeneration, 1, again.Tick)
	first.SwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
		return model.RuntimeEpoch == newGrant.RuntimeEpoch && model.Runners[0].LastProcessedInputSequence == 1 && model.Runners[0].Gravity == seen.Runners[0].Gravity
	})
	result := fixture.SwitchResult(t, second, snapshot.SessionID, fixture.MemberID)
	if result.Status != fausSwitch.ResultCompleted || result.StartedAt == nil || !result.StartedAt.Equal(snapshot.StartedAt) {
		t.Fatalf("result: %+v", result)
	}
}
