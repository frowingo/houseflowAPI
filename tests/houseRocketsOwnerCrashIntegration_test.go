package tests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	rocketsCommands "houseflowApi/internal/application/game/gameSpesific/houseRockets/commands"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/infrastructure/coordination"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/realtime"
)

// A real child owner is killed without room cleanup or lease release. The same
// gateway socket must recover after lease expiry, not rely on a leave command.
func TestHouseRocketsOwnerProcessKillRecoversExistingSocket(t *testing.T) {
	rules := houseRockets.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 0, 0
	fixture := newRocketsGatewayFixture(t, rules)
	snapshot := persistRocketsSession(t, fixture.gameSessionApplicationFixture, true)
	command := exec.Command(os.Args[0], "-test.run=^TestHouseRocketsOwnerProcessHelper$")
	command.Env = append(os.Environ(), "HOUSEFLOW_RECOVERY_HELPER=true", "HOUSEFLOW_RECOVERY_DB="+fixture.db.Name(), "HOUSEFLOW_RECOVERY_SESSION="+snapshot.SessionID)
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
	socket := fixture.connect(t, 0, snapshot.SessionID, fixture.ownerID)
	grant := socket.grant(t)
	before := socket.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhasePlaying && model.ElapsedSeconds > 0.1
	})
	stop()
	var after houseRockets.HouseRocketsSnapshotModel
	var newGrant houseRockets.HouseRocketsControlGrantedModel
	// Grants use a non-coalesced queue and may arrive before the first playing
	// frame. Keep both rather than discarding a grant while awaiting a frame.
	socket.await(t, func(message realtime.ServerMessage) bool {
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
	socket.send(t, houseRockets.SteerMessageType, "postRecoverySteer", houseRockets.HouseRocketsSteerModel{ControlGeneration: newGrant.ControlGeneration, InputSequence: 1, Heading: 0.1})
	socket.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.RuntimeEpoch == newGrant.RuntimeEpoch && model.Players[0].LastProcessedInputSequence == 1
	})
}

func TestHouseRocketsOwnerProcessHelper(t *testing.T) {
	if os.Getenv("HOUSEFLOW_RECOVERY_HELPER") != "true" {
		t.Skip("subprocess owner helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(os.Getenv("HOUSEFLOW_TEST_MONGO_URI")))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	repository := database.NewGameSessionRepository(client, os.Getenv("HOUSEFLOW_RECOVERY_DB"))
	coordinator, err := coordination.NewRedisCoordinator(coordination.RedisOptions{URL: os.Getenv("HOUSEFLOW_TEST_REDIS_URL"), InstanceID: "crashOwnerHelper"})
	if err != nil {
		t.Fatal(err)
	}
	coordinator.Start(ctx)
	mediator := cqrs.New()
	matches := database.NewGameMatchRepository(client, os.Getenv("HOUSEFLOW_RECOVERY_DB"))
	cqrs.MustRegister[houseRockets.HouseRocketsResultModel, rocketsCommands.CompleteMatchCommand](mediator, rocketsCommands.NewCompleteMatchHandler(repository, matches))
	manager, err := realtime.NewRoomManager(coordinator, repository, mediator, realtime.RoomManagerOptions{LeaseTTL: 2 * time.Second, RenewInterval: 100 * time.Millisecond, CommandTimeout: 300 * time.Millisecond, CheckpointInterval: 50 * time.Millisecond, RecoveryScanInterval: -1, MatchRepository: matches})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnsureRoom(ctx, os.Getenv("HOUSEFLOW_RECOVERY_SESSION")); err != nil {
		t.Fatal(err)
	}
	fmt.Println("ownerReady")
	<-ctx.Done()
	closeRoomManager(t, manager)
}
