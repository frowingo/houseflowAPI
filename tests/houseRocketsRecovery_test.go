package tests

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
)

func TestHouseRocketsCheckpointRestoresPhysicsWithoutReplayingInput(t *testing.T) {
	runtime, now := newRocketsRuntime(t)
	grant := bindRocketsControl(t, runtime, "first", "oldConnection", now)
	if err := runtime.Submit(steerRockets(grant, "oldConnection", 1, 0.2, now), now); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Advance(now.Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	checkpoint := runtime.Checkpoint()
	recoveryTime := now.Add(5 * time.Second)
	restored, err := houseRockets.RestoreRuntime(checkpoint, 8, recoveryTime)
	if err != nil {
		t.Fatal(err)
	}
	restoredCheckpoint := restored.Checkpoint()
	if !bytes.Equal(mustJSON(t, checkpoint.World), mustJSON(t, restoredCheckpoint.World)) || restoredCheckpoint.Sequence <= checkpoint.Sequence {
		t.Fatal("physics changed during restore")
	}
	if err := restored.Submit(steerRockets(grant, "oldConnection", 2, 0.9, recoveryTime), recoveryTime); !errors.Is(err, houseRockets.ErrStaleInput) {
		t.Fatalf("old epoch accepted: %v", err)
	}
	input := houseRockets.RuntimeInput{Kind: houseRockets.InputBind, PlayerID: "first", ConnectionID: "newConnection", RuntimeEpoch: 8, CreatedAt: recoveryTime}
	if err := restored.Submit(input, recoveryTime); err != nil {
		t.Fatal(err)
	}
	if err := restored.Advance(recoveryTime); err != nil {
		t.Fatal(err)
	}
	newGrant := (<-restored.Events()).Grant
	if newGrant == nil || newGrant.ControlGeneration == grant.ControlGeneration {
		t.Fatal("generation survived takeover")
	}
	if restored.Checkpoint().Controls[0].ProcessedInput != 0 {
		t.Fatal("old ACK sequence survived new binding")
	}
	if err := restored.Advance(recoveryTime.Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if restored.Checkpoint().World.Tick != checkpoint.World.Tick+6 {
		t.Fatal("owner interruption advanced physics")
	}
	encoded, _ := json.Marshal(restored.RecoveryFrame())
	if bytes.Contains(encoded, []byte(`"checkpoint"`)) || bytes.Contains(encoded, []byte(`"generation"`)) {
		t.Fatal("private checkpoint leaked into fanout")
	}
}

func TestHouseRocketsCheckpointKeepsEliminationAndTerminalProposal(t *testing.T) {
	runtime, now := newRocketsRuntime(t)
	runtime.Reconcile([]string{"second"}, []string{"first", "second"})
	if err := runtime.Advance(now); err != nil {
		t.Fatal(err)
	}
	proposal := <-runtime.Results()
	checkpoint := runtime.Checkpoint()
	restored, err := houseRockets.RestoreRuntime(checkpoint, 8, now.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	recovered := <-restored.Results()
	if !bytes.Equal(mustJSON(t, proposal), mustJSON(t, recovered)) || restored.Checkpoint().World.Players[0].IsAlive {
		t.Fatal("terminal proposal or elimination changed")
	}
	for name, mutate := range map[string]func(*houseRockets.RuntimeCheckpoint){
		"schema":    func(c *houseRockets.RuntimeCheckpoint) { c.SchemaVersion++ },
		"sequence":  func(c *houseRockets.RuntimeCheckpoint) { c.Sequence = 0 },
		"remainder": func(c *houseRockets.RuntimeCheckpoint) { c.Remainder = -1 },
		"controls":  func(c *houseRockets.RuntimeCheckpoint) { c.Controls = nil },
		"proposal":  func(c *houseRockets.RuntimeCheckpoint) { c.ProposedResult = nil },
	} {
		t.Run(name, func(t *testing.T) {
			bad := checkpoint
			mutate(&bad)
			if _, err := houseRockets.RestoreRuntime(bad, 8, now.Add(time.Second)); err == nil {
				t.Fatal("corrupt checkpoint accepted")
			}
		})
	}
}

func TestHouseRocketsRestoreCarriesGraceWithoutExtendingIt(t *testing.T) {
	runtime, now := newRocketsRuntime(t)
	checkpoint := runtime.Checkpoint()
	checkpoint.CapturedAt = checkpoint.Controls[0].GraceEndsAt.Add(-time.Millisecond)
	recoveryTime := now.Add(time.Minute)
	restored, err := houseRockets.RestoreRuntime(checkpoint, 8, recoveryTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Advance(recoveryTime.Add(2 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	result := <-restored.Results()
	if result.EndReason != houseRockets.EndSimultaneousElimination || result.Players[0].EliminationReason == nil || *result.Players[0].EliminationReason != houseRockets.EliminationConnectionExpired {
		t.Fatalf("grace extended: %+v", result)
	}
}

func TestHouseRocketsCheckpointDoesNotReviveEliminatedPlayerInOngoingMatch(t *testing.T) {
	now := time.Now().UTC()
	runtime, err := houseRockets.NewRuntime(houseRockets.NewSimulationParams{SessionID: "ongoingRecovery", HouseID: "house", StartedAt: now, Players: []houseRockets.PlayerIdentity{{PlayerID: "first", DisplayName: "First"}, {PlayerID: "second", DisplayName: "Second"}, {PlayerID: "third", DisplayName: "Third"}}}, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	runtime.Reconcile([]string{"second", "third"}, []string{"first", "second", "third"})
	if err := runtime.Advance(now); err != nil {
		t.Fatal(err)
	}
	checkpoint := runtime.Checkpoint()
	if checkpoint.World.Outcome != nil {
		t.Fatal("ongoing match became terminal")
	}
	recoveredAt := now.Add(time.Second)
	restored, err := houseRockets.RestoreRuntime(checkpoint, 8, recoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustJSON(t, checkpoint.World), mustJSON(t, restored.Checkpoint().World)) {
		t.Fatal("ongoing match changed across recovery")
	}
	if err := restored.Submit(houseRockets.RuntimeInput{Kind: houseRockets.InputBind, RuntimeEpoch: 8, PlayerID: "first", ConnectionID: "cannotRevive", CreatedAt: recoveredAt}, recoveredAt); !errors.Is(err, houseRockets.ErrPlayerEliminated) {
		t.Fatalf("eliminated player regained control: %v", err)
	}
}
