package houseRockets

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/tests/testkit/gamekit"
)

// A real child owner is killed without room cleanup or lease release. The same
// gateway socket must recover after lease expiry, not rely on a leave command.
func TestHouseRocketsOwnerProcessKillRecoversExistingSocket(t *testing.T) {
	rules := houseRockets.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 0, 0
	fixture := gamekit.NewRocketsGatewayFixture(t, rules)
	snapshot := gamekit.PersistRocketsSession(t, fixture.GameSessionApplicationFixture, true)
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
	socket := fixture.Connect(t, 0, snapshot.SessionID, fixture.OwnerID)
	grant := socket.Grant(t)
	before := socket.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhasePlaying && model.ElapsedSeconds > 0.1
	})
	stop()
	var after houseRockets.HouseRocketsSnapshotModel
	var newGrant houseRockets.HouseRocketsControlGrantedModel
	// Grants use a non-coalesced queue and may arrive before the first playing
	// frame. Keep both rather than discarding a grant while awaiting a frame.
	socket.Await(t, func(message realtime.ServerMessage) bool {
		switch message.Type {
		case houseRockets.ControlGrantedMessageType:
			var candidate houseRockets.HouseRocketsControlGrantedModel
			if err := json.Unmarshal(message.Payload, &candidate); err != nil {
				t.Fatal(err)
			}
			if candidate.RuntimeEpoch > grant.RuntimeEpoch {
				newGrant = candidate
			}
		case houseRockets.SnapshotMessageType:
			var candidate houseRockets.HouseRocketsSnapshotModel
			if err := json.Unmarshal(message.Payload, &candidate); err != nil {
				t.Fatal(err)
			}
			if candidate.RuntimeEpoch > before.RuntimeEpoch && candidate.Phase == houseRockets.PhasePlaying {
				after = candidate
			}
		}
		return newGrant.RuntimeEpoch > grant.RuntimeEpoch && after.RuntimeEpoch == newGrant.RuntimeEpoch
	})
	if after.ElapsedSeconds > before.ElapsedSeconds+0.5 || len(after.Players) != len(before.Players) {
		t.Fatalf("recovery advanced interruption or changed roster: before=%+v after=%+v", before, after)
	}
	if newGrant.RuntimeEpoch <= grant.RuntimeEpoch || newGrant.ControlGeneration == grant.ControlGeneration {
		t.Fatal("old control survived process kill")
	}
	socket.Send(t, houseRockets.SteerMessageType, "postRecoverySteer", houseRockets.HouseRocketsSteerModel{ControlGeneration: newGrant.ControlGeneration, InputSequence: 1, Heading: 0.1})
	socket.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.RuntimeEpoch == newGrant.RuntimeEpoch && model.Players[0].LastProcessedInputSequence == 1
	})
}
