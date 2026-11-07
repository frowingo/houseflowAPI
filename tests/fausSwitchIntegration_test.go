package tests

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
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// A race without flips ends by itself in about eight seconds of race time;
// the wait covers that, the countdown and a slow CI machine.
const switchRaceTimeout = 40 * time.Second

// housePlayers returns count house members in join order: the owner, the
// member and new members named like the fixture's users.
func (fixture *rocketsGatewayFixture) housePlayers(t *testing.T, count int) []string {
	t.Helper()
	players := []string{fixture.ownerID, fixture.memberID}
	var added []string
	for index := len(players); index < count; index++ {
		id := primitive.NewObjectID()
		_, err := fixture.db.Collection("User").InsertOne(fixture.ctx, entities.User{Id: id, Firstname: fmt.Sprintf("Player%d", index+1), Lastname: "House",
			Email: id.Hex() + "@test.dev", IsActive: true, HouseIds: []string{fixture.houseID}})
		if err != nil {
			t.Fatal(err)
		}
		added = append(added, id.Hex())
	}
	if len(added) > 0 {
		houseID, _ := primitive.ObjectIDFromHex(fixture.houseID)
		_, err := fixture.db.Collection("House").UpdateOne(fixture.ctx, bson.M{"_id": houseID},
			bson.M{"$push": bson.M{"memberIds": bson.M{"$each": added}}, "$set": bson.M{"maxMemberCount": fausSwitch.MaximumPlayers}})
		if err != nil {
			t.Fatal(err)
		}
	}
	return append(players, added...)[:count]
}

func (socket *rocketsSocket) switchSnapshot(t *testing.T, predicate func(fausSwitch.FausSwitchSnapshotModel) bool) fausSwitch.FausSwitchSnapshotModel {
	t.Helper()
	return socket.switchSnapshotWithin(t, 6*time.Second, predicate)
}

func (socket *rocketsSocket) switchSnapshotWithin(t *testing.T, timeout time.Duration, predicate func(fausSwitch.FausSwitchSnapshotModel) bool) fausSwitch.FausSwitchSnapshotModel {
	t.Helper()
	var snapshot fausSwitch.FausSwitchSnapshotModel
	socket.awaitWithin(t, timeout, func(message realtime.ServerMessage) bool {
		if message.Type != fausSwitch.SnapshotMessageType {
			return false
		}
		snapshot = fausSwitch.FausSwitchSnapshotModel{}
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		return predicate(snapshot)
	})
	return snapshot
}

// liveSwitchSnapshot skips the snapshots already queued and waits for one
// that satisfies the predicate, so a flip is sent for the tick a player sees
// now, inside the rewind window.
func (socket *rocketsSocket) liveSwitchSnapshot(t *testing.T, predicate func(fausSwitch.FausSwitchSnapshotModel) bool) fausSwitch.FausSwitchSnapshotModel {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for queued := true; queued; {
			select {
			case <-socket.messages:
			default:
				queued = false
			}
		}
		if snapshot := socket.switchSnapshot(t, func(fausSwitch.FausSwitchSnapshotModel) bool { return true }); predicate(snapshot) {
			return snapshot
		}
	}
	t.Fatal("no live snapshot matched")
	return fausSwitch.FausSwitchSnapshotModel{}
}

// switchGrant waits for a grant newer than the given epoch.
func (socket *rocketsSocket) switchGrant(t *testing.T, afterEpoch int64) fausSwitch.FausSwitchControlGrantedModel {
	t.Helper()
	var grant fausSwitch.FausSwitchControlGrantedModel
	socket.await(t, func(message realtime.ServerMessage) bool {
		if message.Type != fausSwitch.ControlGrantedMessageType {
			return false
		}
		if err := json.Unmarshal(message.Payload, &grant); err != nil {
			t.Fatal(err)
		}
		return grant.RuntimeEpoch > afterEpoch
	})
	return grant
}

func (socket *rocketsSocket) accepted(t *testing.T, messageID string) {
	t.Helper()
	socket.await(t, func(message realtime.ServerMessage) bool {
		if message.Type == realtime.CommandRejectedMessageType && message.MessageID == messageID {
			t.Fatalf("%s rejected: %+v", messageID, message.Error)
		}
		return message.Type == realtime.CommandAcceptedMessageType && message.MessageID == messageID
	})
}

// sessionColors waits for a session snapshot whose lobby colors are want.
func (socket *rocketsSocket) sessionColors(t *testing.T, want map[string]fausSwitch.Color) {
	t.Helper()
	socket.await(t, func(message realtime.ServerMessage) bool {
		var session dtos.GameSessionResponseModel
		if message.Type != realtime.GameSessionSnapshotEventType || json.Unmarshal(message.Payload, &session) != nil {
			return false
		}
		colors := colorsOf(t, session.LobbyState)
		if len(colors) != len(want) {
			return false
		}
		for id, color := range want {
			if colors[id] != color {
				return false
			}
		}
		return true
	})
}

func (socket *rocketsSocket) flip(t *testing.T, messageID, generation string, sequence, clientTick int64) {
	t.Helper()
	socket.send(t, fausSwitch.FlipMessageType, messageID, fausSwitch.FausSwitchFlipModel{ControlGeneration: generation, InputSequence: sequence, ClientTick: clientTick})
}

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

// switchResult waits for the committed result message and checks it is the
// one the API serves, byte for byte.
func (fixture *rocketsGatewayFixture) switchResult(t *testing.T, socket *rocketsSocket, sessionID, userID string) fausSwitch.FausSwitchResultModel {
	t.Helper()
	message := socket.awaitWithin(t, switchRaceTimeout, func(message realtime.ServerMessage) bool { return message.Type == fausSwitch.ResultMessageType })
	var result fausSwitch.FausSwitchResultModel
	if err := json.Unmarshal(message.Payload, &result); err != nil {
		t.Fatal(err)
	}
	for index := range fixture.urls {
		status, read := fixture.tanksResultHTTP(t, index, sessionID, userID)
		if status != 200 || !bytes.Equal(message.Payload, read) {
			t.Fatalf("HTTP/socket result mismatch on instance %d: %d %s", index, status, read)
		}
	}
	return result
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
	fixture := newGameGatewayFixture(t, definition, 0, 0)
	players := fixture.housePlayers(t, count)
	session := fixture.ensureHTTP(t)
	sockets := make([]*rocketsSocket, count)
	for seat, id := range players {
		sockets[seat] = fixture.connect(t, seat%2, session.SessionId, id)
		sockets[seat].keepAlive(t)
		sockets[seat].lifecycle(t, gameCommands.JoinGameSessionCommandType, fmt.Sprintf("join-%d", seat), struct{}{})
	}
	// Seat s claims the catalog's colors from the end. The last player first
	// asks for the first player's color: the claim is accepted and changes
	// nothing.
	catalog := fausSwitch.PlayerColors()
	want := map[string]fausSwitch.Color{}
	claim := func(seat int, color fausSwitch.Color) {
		t.Helper()
		messageID := fmt.Sprintf("color-%d-%s", seat, color)
		sockets[seat].send(t, fausSwitch.SelectColorMessageType, messageID, fausSwitch.FausSwitchSelectColorModel{Color: color})
		sockets[seat].accepted(t, messageID)
	}
	claim(0, catalog[len(catalog)-1])
	want[players[0]] = catalog[len(catalog)-1]
	sockets[count-1].sessionColors(t, want)
	claim(count-1, catalog[len(catalog)-1])
	for seat := 1; seat < count; seat++ {
		claim(seat, catalog[len(catalog)-1-seat])
		want[players[seat]] = catalog[len(catalog)-1-seat]
	}
	for _, socket := range sockets {
		socket.sessionColors(t, want)
	}
	for seat, socket := range sockets {
		socket.lifecycle(t, gameCommands.SetPlayerReadyCommandType, fmt.Sprintf("ready-%d", seat), dtos.RealtimeReadyModel{Ready: true})
	}
	// The countdown already shows the race: every robot on its start
	// platform in its claimed color, at the race's scale.
	scale := 1.0
	if count > fausSwitch.CrowdedRunnerThreshold {
		scale = fausSwitch.CrowdedRunnerScale
	}
	for _, socket := range sockets {
		countdown := socket.switchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool { return model.Phase == fausSwitch.PhaseCountdown })
		if countdown.Tick != 0 || countdown.CountdownEndsAt == nil || countdown.RunnerScale != scale || countdown.CourseVersion != fausSwitch.CourseVersion ||
			len(countdown.Runners) != count || !sameColors(switchColors(countdown), want) || countdown.Runners[0].DisplayName != "Player0 House" {
			t.Fatalf("countdown: %+v", countdown)
		}
	}
	grants := make([]fausSwitch.FausSwitchControlGrantedModel, count)
	for seat, socket := range sockets {
		grants[seat] = socket.switchGrant(t, 0)
		if grants[seat].PlayerID != players[seat] || grants[seat].RuntimeEpoch != grants[0].RuntimeEpoch {
			t.Fatalf("grant %d: %+v", seat, grants[seat])
		}
	}
	// Each player flips its own robot once while it runs on a rail; the flip
	// is applied at the tick on the player's screen.
	turned := make([]fausSwitch.Gravity, count)
	for seat, socket := range sockets {
		seen := socket.liveSwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
			return model.Phase == fausSwitch.PhasePlaying && model.Tick >= fausSwitch.PhysicsRateHz && standing(model.Runners[seat])
		})
		turned[seat] = fausSwitch.GravityUp + fausSwitch.GravityDown - seen.Runners[seat].Gravity
		socket.flip(t, fmt.Sprintf("flip-%d", seat), grants[seat].ControlGeneration, 1, seen.Tick)
	}
	for seat, socket := range sockets {
		acknowledged := socket.switchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
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
	ended := sockets[0].switchSnapshotWithin(t, switchRaceTimeout, func(model fausSwitch.FausSwitchSnapshotModel) bool { return model.Phase == fausSwitch.PhaseEnded })
	results := make([]fausSwitch.FausSwitchResultModel, count)
	for seat, socket := range sockets {
		results[seat] = fixture.switchResult(t, socket, session.SessionId, players[seat])
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
	stored, err := fixture.repository.FindByID(fixture.ctx, session.SessionId)
	if err != nil || stored.Snapshot().State != gameDomain.SessionFinished || !sameColors(colorsOf(t, stored.Snapshot().LobbyState), want) {
		t.Fatalf("stored session: %v %+v", err, stored.Snapshot())
	}
	if status, _ := fixture.tanksResultHTTP(t, 0, session.SessionId, fixture.outsiderID); status != 403 {
		t.Fatalf("outsider result status: %d", status)
	}
}

// persistSwitchSession stores a running two-player race of the house owner
// and member, with their lobby colors, that started at startedAt.
func persistSwitchSession(t *testing.T, fixture *gameSessionApplicationFixture, startedAt time.Time, colors map[string]fausSwitch.Color) gameDomain.SessionSnapshot {
	t.Helper()
	rules := fausSwitch.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 0, 0
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "switch-runtime-session", HouseID: fixture.houseID, GameKey: fausSwitch.GameKey,
		ProtocolVersion: fausSwitch.ProtocolVersion, Mode: gameDomain.RealtimeGame, Rules: rules, CreatedBy: fixture.ownerID, CreatedAt: startedAt})
	if err != nil {
		t.Fatal(err)
	}
	players := []string{fixture.ownerID, fixture.memberID}
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
	if _, err := fixture.repository.Create(fixture.ctx, session.Snapshot(), session.PendingEvents(), persistenceCommand("switch-create", "create", "switch")); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.repository.FindByID(fixture.ctx, session.Snapshot().SessionID)
	if err != nil {
		t.Fatal(err)
	}
	return stored.Snapshot()
}

// startOwnerProcess runs the session's owner in a child process, which the
// returned function kills.
func startOwnerProcess(t *testing.T, fixture *rocketsGatewayFixture, sessionID string) func() {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestHouseRocketsOwnerProcessHelper$")
	command.Env = append(os.Environ(), "HOUSEFLOW_RECOVERY_HELPER=true", "HOUSEFLOW_RECOVERY_DB="+fixture.db.Name(), "HOUSEFLOW_RECOVERY_SESSION="+sessionID)
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
	fixture := newGameGatewayFixture(t, fausSwitch.Definition(), 0, 0)
	colors := map[string]fausSwitch.Color{fixture.ownerID: fausSwitch.ColorBerry, fixture.memberID: fausSwitch.ColorOcean}
	snapshot := persistSwitchSession(t, fixture.gameSessionApplicationFixture, time.Now().UTC(), colors)
	stop := startOwnerProcess(t, fixture, snapshot.SessionID)
	first := fixture.connect(t, 0, snapshot.SessionID, fixture.ownerID)
	second := fixture.connect(t, 1, snapshot.SessionID, fixture.memberID)
	first.keepAlive(t)
	second.keepAlive(t)
	grant := first.switchGrant(t, 0)
	second.switchGrant(t, 0)
	seen := first.liveSwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
		return model.Phase == fausSwitch.PhasePlaying && model.Tick >= fausSwitch.PhysicsRateHz && standing(model.Runners[0])
	})
	first.flip(t, "flip", grant.ControlGeneration, 1, seen.Tick)
	flipped := first.switchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
		return model.Runners[0].LastProcessedInputSequence == 1
	})
	if flipped.Runners[0].Gravity == seen.Runners[0].Gravity {
		t.Fatalf("flip not applied: %+v", flipped.Runners[0])
	}
	// The checkpoint interval is 50 ms: let one pass after the flip.
	before := first.switchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
		return model.Tick >= flipped.Tick+fausSwitch.PhysicsRateHz/5
	})
	stop()
	newGrant := first.switchGrant(t, grant.RuntimeEpoch)
	second.switchGrant(t, grant.RuntimeEpoch)
	after := first.switchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
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
	first.flip(t, "old-control", grant.ControlGeneration, 2, after.Tick)
	if rejection := first.rejection(t, "old-control"); rejection.Error == nil || rejection.Error.Code != fausSwitch.StaleControlErrorCode {
		t.Fatalf("old generation: %+v", rejection)
	}
	// The new control flips the robot back.
	again := first.liveSwitchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
		return model.RuntimeEpoch == newGrant.RuntimeEpoch && standing(model.Runners[0])
	})
	first.flip(t, "post-recovery", newGrant.ControlGeneration, 1, again.Tick)
	first.switchSnapshot(t, func(model fausSwitch.FausSwitchSnapshotModel) bool {
		return model.RuntimeEpoch == newGrant.RuntimeEpoch && model.Runners[0].LastProcessedInputSequence == 1 && model.Runners[0].Gravity == seen.Runners[0].Gravity
	})
	result := fixture.switchResult(t, second, snapshot.SessionID, fixture.memberID)
	if result.Status != fausSwitch.ResultCompleted || result.StartedAt == nil || !result.StartedAt.Equal(snapshot.StartedAt) {
		t.Fatalf("result: %+v", result)
	}
}
