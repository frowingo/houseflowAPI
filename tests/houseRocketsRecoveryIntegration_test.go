package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	rocketsCommands "houseflowApi/internal/application/game/gameSpesific/houseRockets/commands"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/coordination"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/realtime"
)

func newRecoveryManager(t *testing.T, fixture *gameSessionApplicationFixture, coordinator coordinationAbstract.Coordinator, options realtime.RoomManagerOptions) *realtime.RoomManager {
	t.Helper()
	mediator := newGameSessionMediator(fixture)
	matches := database.NewGameMatchRepository(fixture.db.Client(), fixture.db.Name())
	cqrs.MustRegister[houseRockets.HouseRocketsResultModel, rocketsCommands.CompleteMatchCommand](mediator, rocketsCommands.NewCompleteMatchHandler(fixture.repository, matches))
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.AuthorizeCancellationQuery](mediator, gameQueries.NewAuthorizeCancellationHandler(fixture.repository, fixture.membershipPolicy()))
	options.MatchRepository = matches
	if options.RecoveryScanInterval == 0 {
		options.RecoveryScanInterval = -1
	}
	manager, err := realtime.NewRoomManager(coordinator, fixture.repository, mediator, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRoomManager(t, manager) })
	return manager
}

func awaitRecoveryResult(t *testing.T, fixture *gameSessionApplicationFixture, sessionID string) houseRockets.HouseRocketsResultModel {
	t.Helper()
	matches := database.NewGameMatchRepository(fixture.db.Client(), fixture.db.Name())
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		stored, err := matches.FindResult(fixture.ctx, sessionID)
		if err == nil {
			result, err := houseRockets.DecodeMatchResult(stored)
			if err != nil {
				t.Fatal(err)
			}
			return result
		}
		if !errors.Is(err, gameAbstract.ErrMatchResultNotFound) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatal("recovery result not committed")
		case <-ticker.C:
		}
	}
}

func saveRocketsCheckpoint(t *testing.T, fixture *gameSessionApplicationFixture, coordinator *coordination.RedisCoordinator, snapshot gameDomain.SessionSnapshot, terminal bool) (coordinationAbstract.RoomLease, gameAbstract.RuntimeOwner, houseRockets.RuntimeCheckpoint) {
	t.Helper()
	lease, acquired, err := coordinator.AcquireRoom(fixture.ctx, snapshot.SessionID, 10*time.Second)
	if err != nil || !acquired {
		t.Fatal(err)
	}
	owners := fixture.repository.(gameAbstract.RuntimeOwnerRepository)
	owner, err := owners.ClaimRuntimeOwner(fixture.ctx, 0, gameAbstract.RuntimeOwner{SessionID: snapshot.SessionID, OwnerInstanceID: lease.OwnerInstanceID, LeaseID: lease.LeaseID})
	if err != nil {
		t.Fatal(err)
	}
	if err := owners.MarkRuntimeStarted(fixture.ctx, owner); err != nil {
		t.Fatal(err)
	}
	runtime, err := houseRockets.NewRuntime(houseRockets.NewSimulationParams{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, StartedAt: snapshot.StartedAt, Players: []houseRockets.PlayerIdentity{{PlayerID: fixture.ownerID, DisplayName: "Owner"}, {PlayerID: fixture.memberID, DisplayName: "Member"}}}, owner.Generation, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if terminal {
		runtime.Reconcile([]string{fixture.memberID}, []string{fixture.ownerID, fixture.memberID})
		if err := runtime.Advance(time.Now()); err != nil {
			t.Fatal(err)
		}
	} else {
		time.Sleep(100 * time.Millisecond)
		if err := runtime.Advance(time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint := runtime.Checkpoint()
	payload, _ := json.Marshal(checkpoint)
	if err := coordinator.SaveRuntimeCheckpoint(fixture.ctx, lease, coordinationAbstract.RuntimeCheckpoint{Epoch: checkpoint.Epoch, Sequence: checkpoint.Sequence, CapturedAt: checkpoint.CapturedAt, Payload: payload}, time.Minute); err != nil {
		t.Fatal(err)
	}
	return lease, owner, checkpoint
}

func TestHouseRocketsRecoveryRestoresTerminalProposalAndCleansCheckpoint(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	snapshot := persistRocketsSession(t, fixture, true)
	stored, _ := fixture.repository.FindByID(fixture.ctx, snapshot.SessionID)
	snapshot = stored.Snapshot()
	url := prepareRedis(t)
	first := newTestCoordinator(t, url, "terminalFirst")
	second := newTestCoordinator(t, url, "terminalSecond")
	lease, _, checkpoint := saveRocketsCheckpoint(t, fixture, first, snapshot, true)
	if _, err := first.ReleaseRoom(fixture.ctx, lease); err != nil {
		t.Fatal(err)
	}
	manager := newRecoveryManager(t, fixture, second, realtime.RoomManagerOptions{})
	if _, err := manager.EnsureRoom(fixture.ctx, snapshot.SessionID); err != nil {
		t.Fatal(err)
	}
	result := awaitRecoveryResult(t, fixture, snapshot.SessionID)
	if !bytes.Equal(mustJSON(t, result), mustJSON(t, checkpoint.ProposedResult)) {
		t.Fatal("terminal result changed across takeover")
	}
	if result.WinnerID == nil || *result.WinnerID != fixture.memberID || result.Players[0].EliminatedAtTick == nil {
		t.Fatal("elimination lost across takeover")
	}
	if _, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, houseRockets.GameKey); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("active slot retained: %v", err)
	}
	closeRoomManager(t, manager)
	cleanupLease, acquired, err := second.AcquireRoom(fixture.ctx, snapshot.SessionID, time.Second)
	if err != nil || !acquired {
		t.Fatalf("finished lease not released: %v", err)
	}
	if _, found, err := second.LoadRuntimeCheckpoint(fixture.ctx, cleanupLease); err != nil || found {
		t.Fatalf("finished checkpoint retained: %t %v", found, err)
	}
	if _, err := second.ReleaseRoom(fixture.ctx, cleanupLease); err != nil {
		t.Fatal(err)
	}
	newManager := newRecoveryManager(t, fixture, first, realtime.RoomManagerOptions{})
	if _, err := newManager.EnsureRoom(fixture.ctx, snapshot.SessionID); !errors.Is(err, realtime.ErrTerminalRoom) {
		t.Fatal("finished session restarted")
	}
}

func TestHouseRocketsRecoveryNeverSpawnsLongOrphanedRunningSession(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	startedAt := time.Now().UTC().Add(-time.Minute)
	rules := houseRockets.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 0, 0
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "neverSpawnedSession", HouseID: fixture.houseID, GameKey: houseRockets.GameKey, ProtocolVersion: houseRockets.ProtocolVersion, Mode: gameDomain.RealtimeGame, Rules: rules, CreatedBy: fixture.ownerID, CreatedAt: startedAt})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fixture.ownerID, fixture.memberID} {
		if err := session.JoinPlayer(id, startedAt); err != nil {
			t.Fatal(err)
		}
		if err := session.SetReady(id, true, startedAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.repository.Create(fixture.ctx, session.Snapshot(), session.PendingEvents(), persistenceCommand("oldNeverSpawnedCreate", "create", "rockets")); err != nil {
		t.Fatal(err)
	}
	coordinator := newTestCoordinator(t, prepareRedis(t), "neverSpawnedRecovery")
	newRecoveryManager(t, fixture, coordinator, realtime.RoomManagerOptions{RecoveryScanInterval: 50 * time.Millisecond})
	result := awaitRecoveryResult(t, fixture, session.Snapshot().SessionID)
	if result.EndReason != houseRockets.EndRecoveryFailed || result.Status != houseRockets.ResultCancelled || result.DurationSeconds != 0 {
		t.Fatalf("long orphan spawned new game: %+v", result)
	}
}

func TestHouseRocketsRecoveryMissingStateCancelsWithoutSpawn(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	snapshot := persistRocketsSession(t, fixture, true)
	owners := fixture.repository.(gameAbstract.RuntimeOwnerRepository)
	owner, err := owners.ClaimRuntimeOwner(fixture.ctx, 0, gameAbstract.RuntimeOwner{SessionID: snapshot.SessionID, OwnerInstanceID: "lostOwner", LeaseID: "lostLease"})
	if err != nil {
		t.Fatal(err)
	}
	if err := owners.MarkRuntimeStarted(fixture.ctx, owner); err != nil {
		t.Fatal(err)
	}
	coordinator := newTestCoordinator(t, prepareRedis(t), "missingStateRecovery")
	newRecoveryManager(t, fixture, coordinator, realtime.RoomManagerOptions{RecoveryScanInterval: 50 * time.Millisecond})
	// No client, socket or command activates this room: the indexed scanner does.
	result := awaitRecoveryResult(t, fixture, snapshot.SessionID)
	if result.Status != houseRockets.ResultCancelled || result.EndReason != houseRockets.EndRecoveryFailed || result.WinnerID != nil || result.DurationSeconds != 0 {
		t.Fatalf("missing state produced a game: %+v", result)
	}
}

func TestHouseRocketsRecoveryStaleStateCancelsAndFencesOldIntent(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	snapshot := persistRocketsSession(t, fixture, true)
	stored, _ := fixture.repository.FindByID(fixture.ctx, snapshot.SessionID)
	snapshot = stored.Snapshot()
	url := prepareRedis(t)
	first := newTestCoordinator(t, url, "staleFirst")
	second := newTestCoordinator(t, url, "staleSecond")
	lease, owner, _ := saveRocketsCheckpoint(t, fixture, first, snapshot, false)
	if _, err := first.ReleaseRoom(fixture.ctx, lease); err != nil {
		t.Fatal(err)
	}
	manager := newRecoveryManager(t, fixture, second, realtime.RoomManagerOptions{RecoveryMaxAge: time.Nanosecond})
	if _, err := manager.EnsureRoom(fixture.ctx, snapshot.SessionID); err != nil {
		t.Fatal(err)
	}
	result := awaitRecoveryResult(t, fixture, snapshot.SessionID)
	if result.EndReason != houseRockets.EndRecoveryFailed || result.Status != houseRockets.ResultCancelled {
		t.Fatalf("stale state resumed: %+v", result)
	}
	trigger := persistenceCommand("staleTrigger", gameCommands.CancelGameSessionCommandType, "payload")
	if err := fixture.repository.(gameAbstract.RuntimeOwnerRepository).SaveCompletionTrigger(fixture.ctx, owner, trigger); !errors.Is(err, gameAbstract.ErrRuntimeOwnerConflict) {
		t.Fatalf("stale intent accepted: %v", err)
	}
}

func TestHouseRocketsRecoveryKeepsAuthorizedCancellationReceipt(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	snapshot := persistRocketsSession(t, fixture, true)
	url := prepareRedis(t)
	first := newTestCoordinator(t, url, "intentFirst")
	second := newTestCoordinator(t, url, "intentSecond")
	lease, owner, _ := saveRocketsCheckpoint(t, fixture, first, snapshot, false)
	payload, _ := json.Marshal(struct{ SessionID string }{snapshot.SessionID})
	digest := sha256.Sum256(payload)
	trigger := gameAbstract.CommandDescriptor{CommandID: "cancelBeforeCrash", ActorID: fixture.ownerID, CommandType: gameCommands.CancelGameSessionCommandType, PayloadHash: hex.EncodeToString(digest[:])}
	if err := fixture.repository.(gameAbstract.RuntimeOwnerRepository).SaveCompletionTrigger(fixture.ctx, owner, trigger); err != nil {
		t.Fatal(err)
	}
	if _, err := first.ReleaseRoom(fixture.ctx, lease); err != nil {
		t.Fatal(err)
	}
	manager := newRecoveryManager(t, fixture, second, realtime.RoomManagerOptions{})
	if _, err := manager.EnsureRoom(fixture.ctx, snapshot.SessionID); err != nil {
		t.Fatal(err)
	}
	result := awaitRecoveryResult(t, fixture, snapshot.SessionID)
	if result.EndReason != houseRockets.EndCancelledByUser || result.Status != houseRockets.ResultCancelled {
		t.Fatalf("cancel intent lost: %+v", result)
	}
	count, err := fixture.db.Collection(entities.GameSessionCommandReceiptCollectionName).CountDocuments(fixture.ctx, bson.M{"commandId": trigger.CommandID, "actorId": trigger.ActorID})
	if err != nil || count != 1 {
		t.Fatalf("cancel receipt lost: %d %v", count, err)
	}
}

type unavailableCheckpointCoordinator struct {
	*coordination.RedisCoordinator
	unavailable atomic.Bool
}

func (coordinator *unavailableCheckpointCoordinator) SaveRuntimeCheckpoint(ctx context.Context, lease coordinationAbstract.RoomLease, checkpoint coordinationAbstract.RuntimeCheckpoint, ttl time.Duration) error {
	if coordinator.unavailable.Load() {
		return coordinationAbstract.ErrUnavailable
	}
	return coordinator.RedisCoordinator.SaveRuntimeCheckpoint(ctx, lease, checkpoint, ttl)
}

func TestHouseRocketsRecoveryCriticalWriteFailureNeverAnnouncesElimination(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	snapshot := persistRocketsSession(t, fixture, true)
	url := prepareRedis(t)
	first := &unavailableCheckpointCoordinator{RedisCoordinator: newTestCoordinator(t, url, "checkpointFailureFirst")}
	second := newTestCoordinator(t, url, "checkpointFailureSecond")
	events, err := second.SubscribeRoomEvents(fixture.ctx, snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	manager := newRecoveryManager(t, fixture, first, realtime.RoomManagerOptions{})
	if _, err := manager.EnsureRoom(fixture.ctx, snapshot.SessionID); err != nil {
		t.Fatal(err)
	}
	first.unavailable.Store(true)
	// A durable leave survives failure of the critical Redis barrier. Recovery
	// must apply it before publishing physics, even from the earlier checkpoint.
	if err := manager.Dispatch(fixture.ctx, coordinationAbstract.MessageEnvelope{MessageID: "leaveBeforeBarrierFailure", RoomID: snapshot.SessionID, ActorID: fixture.ownerID, Type: gameCommands.LeaveGameSessionCommandType, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
waitFailure:
	for {
		select {
		case reported := <-manager.Errors():
			if errors.Is(reported.Err, coordinationAbstract.ErrUnavailable) {
				break waitFailure
			}
		case event := <-events.Messages():
			if event.Type == houseRockets.ResultMessageType {
				t.Fatal("result announced without checkpoint protection")
			}
			if event.Type == realtime.GameRuntimeSnapshotEventType {
				var frame houseRockets.RuntimeFrame
				if err := json.Unmarshal(event.Payload, &frame); err != nil {
					t.Fatal(err)
				}
				for _, player := range frame.World.Players {
					if !player.IsAlive {
						t.Fatal("unprotected elimination announced")
					}
				}
			}
		case <-deadline.C:
			t.Fatal("checkpoint failure did not stop owner")
		}
	}
	closeRoomManager(t, manager)
	recovered := newRecoveryManager(t, fixture, second, realtime.RoomManagerOptions{})
	if _, err := recovered.EnsureRoom(fixture.ctx, snapshot.SessionID); err != nil {
		t.Fatal(err)
	}
	result := awaitRecoveryResult(t, fixture, snapshot.SessionID)
	if result.WinnerID == nil || *result.WinnerID != fixture.memberID || result.Players[0].EliminationReason == nil || *result.Players[0].EliminationReason != houseRockets.EliminationForfeit {
		t.Fatalf("durable forfeit lost across failed checkpoint: %+v", result)
	}
}

func TestHouseRocketsRecoveryRedisResetCannotRestartOrAuthorizeOldOwner(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	snapshot := persistRocketsSession(t, fixture, true)
	url := prepareRedis(t)
	first := newTestCoordinator(t, url, "resetFirst")
	second := newTestCoordinator(t, url, "resetSecond")
	lease, oldOwner, checkpoint := saveRocketsCheckpoint(t, fixture, first, snapshot, false)
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	if err := client.FlushDB(fixture.ctx).Err(); err != nil {
		t.Fatal(err)
	}
	manager := newRecoveryManager(t, fixture, second, realtime.RoomManagerOptions{})
	ownership, err := manager.EnsureRoom(fixture.ctx, snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if ownership.RuntimeEpoch <= oldOwner.Generation {
		t.Fatal("Redis reset reset durable epoch")
	}
	payload, _ := json.Marshal(checkpoint)
	if err := first.SaveRuntimeCheckpoint(fixture.ctx, lease, coordinationAbstract.RuntimeCheckpoint{Epoch: checkpoint.Epoch, Sequence: checkpoint.Sequence + 1, CapturedAt: checkpoint.CapturedAt, Payload: payload}, time.Minute); !errors.Is(err, coordinationAbstract.ErrLeaseLost) {
		t.Fatalf("old owner wrote after reset: %v", err)
	}
	result := awaitRecoveryResult(t, fixture, snapshot.SessionID)
	if result.Status != houseRockets.ResultCancelled || result.EndReason != houseRockets.EndRecoveryFailed || result.WinnerID != nil {
		t.Fatalf("Redis reset respawned match: %+v", result)
	}
}

func TestHouseRocketsRecoveryCorruptCheckpointCancels(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	snapshot := persistRocketsSession(t, fixture, true)
	url := prepareRedis(t)
	first := newTestCoordinator(t, url, "corruptFirst")
	second := newTestCoordinator(t, url, "corruptSecond")
	lease, _, checkpoint := saveRocketsCheckpoint(t, fixture, first, snapshot, false)
	if err := first.SaveRuntimeCheckpoint(fixture.ctx, lease, coordinationAbstract.RuntimeCheckpoint{Epoch: checkpoint.Epoch, Sequence: checkpoint.Sequence + 1, CapturedAt: checkpoint.CapturedAt, Payload: json.RawMessage(`{"schemaVersion":999}`)}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := first.ReleaseRoom(fixture.ctx, lease); err != nil {
		t.Fatal(err)
	}
	manager := newRecoveryManager(t, fixture, second, realtime.RoomManagerOptions{})
	if _, err := manager.EnsureRoom(fixture.ctx, snapshot.SessionID); err != nil {
		t.Fatal(err)
	}
	result := awaitRecoveryResult(t, fixture, snapshot.SessionID)
	if result.Status != houseRockets.ResultCancelled || result.EndReason != houseRockets.EndRecoveryFailed {
		t.Fatalf("corrupt state resumed: %+v", result)
	}
}

func TestHouseRocketsRecoveryFinalizationBudgetPreservesProposalForNextOwner(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	snapshot := persistRocketsSession(t, fixture, true)
	stored, _ := fixture.repository.FindByID(fixture.ctx, snapshot.SessionID)
	snapshot = stored.Snapshot()
	url := prepareRedis(t)
	first := newTestCoordinator(t, url, "boundedFirst")
	second := newTestCoordinator(t, url, "boundedSecond")
	third := newTestCoordinator(t, url, "boundedThird")
	lease, _, checkpoint := saveRocketsCheckpoint(t, fixture, first, snapshot, true)
	if _, err := first.ReleaseRoom(fixture.ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.RunCommand(fixture.ctx, bson.D{{Key: "collMod", Value: entities.OutboxMessageCollectionName}, {Key: "validator", Value: bson.M{"eventType": bson.M{"$ne": houseRockets.ResultMessageType}}}}).Err(); err != nil {
		t.Fatal(err)
	}
	manager := newRecoveryManager(t, fixture, second, realtime.RoomManagerOptions{FinalizationTimeout: 400 * time.Millisecond})
	if _, err := manager.EnsureRoom(fixture.ctx, snapshot.SessionID); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
waitBudget:
	for {
		select {
		case reported := <-manager.Errors():
			if errors.Is(reported.Err, context.DeadlineExceeded) {
				break waitBudget
			}
		case <-deadline.C:
			t.Fatal("finalization exceeded bounded owner lifetime")
		}
	}
	closeRoomManager(t, manager)
	if _, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, houseRockets.GameKey); err != nil {
		t.Fatalf("failed transaction released active slot: %v", err)
	}
	if err := fixture.db.RunCommand(fixture.ctx, bson.D{{Key: "collMod", Value: entities.OutboxMessageCollectionName}, {Key: "validator", Value: bson.M{}}}).Err(); err != nil {
		t.Fatal(err)
	}
	recovered := newRecoveryManager(t, fixture, third, realtime.RoomManagerOptions{RecoveryMaxAge: time.Nanosecond})
	if _, err := recovered.EnsureRoom(fixture.ctx, snapshot.SessionID); err != nil {
		t.Fatal(err)
	}
	result := awaitRecoveryResult(t, fixture, snapshot.SessionID)
	if !bytes.Equal(mustJSON(t, result), mustJSON(t, checkpoint.ProposedResult)) {
		t.Fatal("finalization retry changed proposed result")
	}
}

func TestHouseRocketsRecoveryIdleLobbyReleasesMemoryAndLeaseNotDiscovery(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	snapshot := persistRocketsSession(t, fixture, false)
	coordinator := newTestCoordinator(t, prepareRedis(t), "idleLobby")
	manager := newRecoveryManager(t, fixture, coordinator, realtime.RoomManagerOptions{IdleRoomTimeout: 50 * time.Millisecond, RecoveryScanInterval: 20 * time.Millisecond})
	first, err := manager.EnsureRoom(fixture.ctx, snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool {
		_, exists, err := coordinator.CurrentRoomOwner(fixture.ctx, snapshot.SessionID)
		return err == nil && !exists
	})
	active, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, houseRockets.GameKey)
	if err != nil || active.State() != gameDomain.SessionLobby {
		t.Fatalf("idle lobby removed from discovery: %v", err)
	}
	// Scanner must not continuously reactivate idle lobbies without clients.
	time.Sleep(100 * time.Millisecond)
	_, exists, err := coordinator.CurrentRoomOwner(fixture.ctx, snapshot.SessionID)
	if err != nil || exists {
		t.Fatalf("scanner reactivated empty lobby: %v", err)
	}
	next, err := manager.EnsureRoom(fixture.ctx, snapshot.SessionID)
	if err != nil || next.RuntimeEpoch <= first.RuntimeEpoch {
		t.Fatalf("lobby cannot reactivate: %+v %v", next, err)
	}
}
