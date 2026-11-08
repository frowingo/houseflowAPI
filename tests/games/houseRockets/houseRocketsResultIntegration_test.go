package houseRockets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/data/migrations"
	infrastructureCoordination "houseflowApi/internal/infrastructure/coordination"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/tests/testkit"
	"houseflowApi/tests/testkit/gamekit"

	"go.mongodb.org/mongo-driver/bson"
)

func TestHouseRocketsGatewayKeepsFinalizingDuringTransactionFailure(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := gamekit.PersistRocketsSession(t, fixture.GameSessionApplicationFixture, true)
	first := fixture.Connect(t, 0, session.SessionID, fixture.OwnerID)
	second := fixture.Connect(t, 1, session.SessionID, fixture.MemberID)
	_ = first.Grant(t)
	_ = second.Grant(t)
	rejectMatchResults(t, fixture.GameSessionApplicationFixture)
	first.Send(t, gameCommands.CancelGameSessionCommandType, "pending-cancel", struct{}{})
	finalizing := second.Snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhaseFinalizing
	})
	if finalizing.WinnerID != nil {
		t.Fatal("winner published before commit")
	}
	// Retry of the same cancel is held until the shared transaction commits.
	first.Send(t, gameCommands.CancelGameSessionCommandType, "pending-cancel", struct{}{})
	deadline := time.NewTimer(800 * time.Millisecond)
	defer deadline.Stop()
waitForRetry:
	for {
		select {
		case message := <-second.Messages:
			if message.Type == houseRockets.ResultMessageType {
				t.Fatal("result event escaped rejected transaction")
			}
		case <-deadline.C:
			break waitForRetry
		}
	}
	if status, _ := fixture.ResultHTTP(t, 1, session.SessionID, fixture.MemberID); status != 404 {
		t.Fatalf("uncommitted HTTP result: %d", status)
	}
	active, err := fixture.Repository.FindActive(fixture.Ctx, fixture.HouseID, houseRockets.GameKey)
	if err != nil || active.Snapshot().State != gameDomain.SessionRunning {
		t.Fatalf("active slot released before commit: %v", err)
	}
	acceptMatchResults(t, fixture.GameSessionApplicationFixture)
	second.Await(t, func(message realtime.ServerMessage) bool { return message.Type == houseRockets.ResultMessageType })
	if status, _ := fixture.ResultHTTP(t, 1, session.SessionID, fixture.MemberID); status != 200 {
		t.Fatalf("retry never completed: %d", status)
	}
}

type resultPublishFailureCoordinator struct {
	*infrastructureCoordination.RedisCoordinator
}

func (coordinator resultPublishFailureCoordinator) PublishRoomEvent(ctx context.Context, lease coordinationAbstract.RoomLease, event coordinationAbstract.MessageEnvelope) error {
	if event.Type == houseRockets.ResultMessageType {
		return coordinationAbstract.ErrUnavailable
	}
	return coordinator.RedisCoordinator.PublishRoomEvent(ctx, lease, event)
}

// rejectMatchResults fails the last write of the completion transaction.
func rejectMatchResults(t *testing.T, fixture *gamekit.GameSessionApplicationFixture) {
	t.Helper()
	if err := fixture.DB.RunCommand(fixture.Ctx, bson.D{{Key: "collMod", Value: database.GameMatchResultCollectionName}, {Key: "validator", Value: bson.M{"_id": bson.M{"$exists": false}}}}).Err(); err != nil {
		t.Fatal(err)
	}
}

// acceptMatchResults lifts the injected failure; the disposable test database
// keeps no result schema validator afterwards.
func acceptMatchResults(t *testing.T, fixture *gamekit.GameSessionApplicationFixture) {
	t.Helper()
	if err := fixture.DB.RunCommand(fixture.Ctx, bson.D{{Key: "collMod", Value: database.GameMatchResultCollectionName}, {Key: "validator", Value: bson.M{}}}).Err(); err != nil {
		t.Fatal(err)
	}
}

func TestHouseRocketsResultSurvivesPostCommitPublishFailure(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	session := gamekit.PersistRocketsSession(t, fixture, true)
	matches := database.NewGameMatchRepository(fixture.DB.Client(), fixture.DB.Name())
	mediator := gamekit.NewGameSessionMediator(fixture)
	gamekit.RegisterRocketsResults(mediator, fixture.Repository, matches, nil)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.AuthorizeCancellationQuery](mediator, gameQueries.NewAuthorizeCancellationHandler(fixture.Repository, fixture.MembershipPolicy()))
	coordinator := resultPublishFailureCoordinator{gamekit.NewTestCoordinator(t, testkit.PrepareRedis(t), "result-publish-failure")}
	manager, err := realtime.NewRoomManager(coordinator, fixture.Repository, mediator, realtime.RoomManagerOptions{Games: gamekit.RocketsGames(), MatchRepository: matches, RenewInterval: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Start(fixture.Ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gamekit.CloseRoomManager(t, manager) })
	if _, err = manager.EnsureRoom(fixture.Ctx, session.SessionID); err != nil {
		t.Fatal(err)
	}
	if err = manager.Dispatch(fixture.Ctx, coordinationAbstract.MessageEnvelope{MessageID: "cancel-before-publish-failure", RoomID: session.SessionID, ActorID: fixture.OwnerID, Type: gameCommands.CancelGameSessionCommandType}); err != nil {
		t.Fatal(err)
	}
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case failure := <-manager.Errors():
			if errors.Is(failure.Err, coordinationAbstract.ErrUnavailable) {
				result, err := matches.FindResult(fixture.Ctx, session.SessionID)
				if err != nil || result.Status != gameDomain.MatchCancelled {
					t.Fatalf("result lost after commit: %+v %v", result, err)
				}
				if _, err := fixture.Repository.FindActive(fixture.Ctx, fixture.HouseID, houseRockets.GameKey); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
					t.Fatalf("committed slot retained: %v", err)
				}
				return
			}
		case <-timeout.C:
			t.Fatal("publish failure was not observed")
		}
	}
}

func TestHouseRocketsResultMigrationIsRepeatableAndRejectsInvalidDocuments(t *testing.T) {
	fixture := gamekit.NewGameSessionPersistenceFixture(t)
	for _, migration := range migrations.AllMigrations() {
		if migration.Version() == "0042" {
			for range 2 {
				if err := migration.Up(fixture.Ctx, fixture.DB); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if _, err := fixture.DB.Collection(database.GameMatchResultCollectionName).InsertOne(fixture.Ctx, bson.M{"_id": "invalid", "runtimeEpoch": int64(0)}); err == nil {
		t.Fatal("invalid result passed schema validator")
	}
	count, err := fixture.DB.Collection("localization").CountDocuments(fixture.Ctx, bson.M{"key": "houseRockets.error.result_not_found"})
	if err != nil || count != 2 {
		t.Fatalf("localization duplication: %d %v", count, err)
	}
}

func completionFixture(t *testing.T, reason houseRockets.EndReason) (*gamekit.GameSessionApplicationFixture, *database.GameMatchRepository, gamekit.RocketsCompletion) {
	t.Helper()
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	snapshot := gamekit.PersistRocketsSession(t, fixture, true)
	stored, err := fixture.Repository.FindByID(fixture.Ctx, snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot = stored.Snapshot()
	owners := fixture.Repository.(gameAbstract.RuntimeOwnerRepository)
	owner, err := owners.ClaimRuntimeOwner(fixture.Ctx, 0, gameAbstract.RuntimeOwner{SessionID: snapshot.SessionID, OwnerInstanceID: "result-owner", LeaseID: "result-lease"})
	if err != nil {
		t.Fatal(err)
	}
	if err := owners.MarkRuntimeStarted(fixture.Ctx, owner); err != nil {
		t.Fatal(err)
	}
	simulation, err := houseRockets.NewSimulation(houseRockets.NewSimulationParams{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, StartedAt: snapshot.StartedAt, Players: []houseRockets.PlayerIdentity{{PlayerID: fixture.OwnerID, DisplayName: "Owner"}, {PlayerID: fixture.MemberID, DisplayName: "Member"}}})
	if err != nil {
		t.Fatal(err)
	}
	switch reason {
	case houseRockets.EndLastSurvivor:
		err = simulation.EliminatePlayers([]string{fixture.OwnerID}, houseRockets.EliminationForfeit)
	case houseRockets.EndSimultaneousElimination:
		err = simulation.EliminatePlayers([]string{fixture.OwnerID, fixture.MemberID}, houseRockets.EliminationConnectionExpired)
	case houseRockets.EndSessionExpired:
		state := simulation.State()
		state.Tick = houseRockets.MaximumMatchTicks
		state.Outcome = &houseRockets.SimulationOutcome{Status: houseRockets.ResultCancelled, EndReason: houseRockets.EndSessionExpired}
		simulation, err = houseRockets.RestoreSimulation(state)
	default:
		err = simulation.Cancel(reason)
	}
	if err != nil {
		t.Fatal(err)
	}
	endedAt := time.Now().UTC()
	minimumEnd := snapshot.StartedAt.Add(time.Duration(simulation.State().Tick) * time.Second / houseRockets.PhysicsRateHz)
	if endedAt.Before(minimumEnd) {
		endedAt = minimumEnd
	}
	result, err := simulation.ProposeResult(endedAt)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, database.NewGameMatchRepository(fixture.DB.Client(), fixture.DB.Name()), gamekit.RocketsCompletion{Result: result, Owner: owner}
}

func TestHouseRocketsResultFiveMinuteExpiryIsPersistedWithoutWinner(t *testing.T) {
	fixture, matches, command := completionFixture(t, houseRockets.EndSessionExpired)
	result, err := gamekit.NewRocketsCompletionHandler(fixture.Repository, matches).Handle(fixture.Ctx, command)
	if err != nil || result.Status != houseRockets.ResultCancelled || result.DurationSeconds != 300 || result.WinnerID != nil {
		t.Fatalf("expiry result: %+v %v", result, err)
	}
	for _, player := range result.Players {
		if player.Rank != nil {
			t.Fatal("expiry produced leaderboard rank")
		}
	}
	stored, err := fixture.Repository.FindByID(fixture.Ctx, result.SessionID)
	if err != nil || stored.Snapshot().State != gameDomain.SessionCancelled {
		t.Fatalf("expiry session: %v", err)
	}
	// A cancelled match leaves nothing behind: session and result expire together.
	want := stored.Snapshot().EndedAt.Add(database.CancelledSessionRetention).Truncate(time.Millisecond)
	for _, collection := range []string{database.GameSessionCollectionName, database.GameMatchResultCollectionName} {
		var document struct {
			ExpiresAt *time.Time `bson:"expiresAt"`
		}
		if err := fixture.DB.Collection(collection).FindOne(fixture.Ctx, bson.M{"_id": result.SessionID}).Decode(&document); err != nil || document.ExpiresAt == nil || !document.ExpiresAt.Equal(want) {
			t.Fatalf("%s expiresAt = %v, want %s: %v", collection, document.ExpiresAt, want, err)
		}
	}
}

func TestHouseRocketsGatewayPregameCancellationDoesNotCreateResult(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := fixture.EnsureHTTP(t)
	first := fixture.Connect(t, 0, session.SessionId, fixture.OwnerID)
	first.Lifecycle(t, gameCommands.CancelGameSessionCommandType, "pregame-cancel", struct{}{})
	if status, _ := fixture.ResultHTTP(t, 1, session.SessionId, fixture.MemberID); status != 404 {
		t.Fatalf("pregame result: %d", status)
	}
	rematch := fixture.EnsureHTTP(t)
	if rematch.SessionId == session.SessionId {
		t.Fatal("pregame cancellation retained active slot")
	}
}

func TestHouseRocketsResultConcurrentCompletionIsImmutableAndRematchSafe(t *testing.T) {
	fixture, matches, command := completionFixture(t, houseRockets.EndLastSurvivor)
	handler := gamekit.NewRocketsCompletionHandler(fixture.Repository, matches)
	start := make(chan struct{})
	var workers sync.WaitGroup
	errorsChannel := make(chan error, 4)
	results := make(chan houseRockets.HouseRocketsResultModel, 4)
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result, err := handler.Handle(fixture.Ctx, command)
			errorsChannel <- err
			results <- result
		}()
	}
	close(start)
	workers.Wait()
	close(errorsChannel)
	close(results)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if !bytes.Equal(gamekit.MustJSON(t, result), gamekit.MustJSON(t, command.Result)) {
			t.Fatalf("canonical result changed: %+v", result)
		}
	}
	// One canonical result; the runtime completion itself keeps no receipt.
	for collection, want := range map[string]int64{database.GameMatchResultCollectionName: 1, entities.GameSessionCommandReceiptCollectionName: 0} {
		filter := bson.M{"_id": command.Result.SessionID}
		if collection == entities.GameSessionCommandReceiptCollectionName {
			filter = bson.M{"commandId": "complete:" + command.Result.SessionID}
		}
		count, err := fixture.DB.Collection(collection).CountDocuments(fixture.Ctx, filter)
		if err != nil || count != want {
			t.Fatalf("%s count=%d want=%d: %v", collection, count, want, err)
		}
	}
	stored, err := fixture.Repository.FindByID(fixture.Ctx, command.Result.SessionID)
	if err != nil || stored.Snapshot().State != gameDomain.SessionFinished {
		t.Fatalf("terminal session: %v", err)
	}
	if _, err := fixture.Repository.FindActive(fixture.Ctx, fixture.HouseID, houseRockets.GameKey); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("active slot retained: %v", err)
	}
	owners := fixture.Repository.(gameAbstract.RuntimeOwnerRepository)
	if _, err := owners.ClaimRuntimeOwner(fixture.Ctx, command.Owner.Generation, gameAbstract.RuntimeOwner{SessionID: command.Result.SessionID, OwnerInstanceID: "new-owner", LeaseID: "new-lease"}); !errors.Is(err, gameAbstract.ErrRuntimeOwnerConflict) {
		t.Fatalf("completed owner reclaimed: %v", err)
	}
	// Create another lobby in the same slot; replaying old completion cannot delete it.
	newSession, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "rematch-session", HouseID: fixture.HouseID, GameKey: houseRockets.GameKey, ProtocolVersion: 2, Mode: gameDomain.RealtimeGame, Rules: houseRockets.Definition().Rules, CreatedBy: fixture.OwnerID, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Repository.Create(fixture.Ctx, newSession.Snapshot(), newSession.PendingEvents(), gamekit.PersistenceCommand("rematch-create", "create", "new-lobby")); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Handle(fixture.Ctx, command); err != nil {
		t.Fatal(err)
	}
	active, err := fixture.Repository.FindActive(fixture.Ctx, fixture.HouseID, houseRockets.GameKey)
	if err != nil || active.Snapshot().SessionID != "rematch-session" {
		t.Fatalf("old completion deleted new slot: %v", err)
	}
	command.Result.Players[0].Distance = 1
	if _, err := handler.Handle(fixture.Ctx, command); err == nil {
		t.Fatal("different result overwrote canonical result")
	}
}

func TestHouseRocketsResultTransactionRollbackAndRetry(t *testing.T) {
	fixture, matches, command := completionFixture(t, houseRockets.EndCancelledByUser)
	command.CancelCommandID, command.CancelUserID = "cancel-match", fixture.OwnerID
	handler := gamekit.NewRocketsCompletionHandler(fixture.Repository, matches)
	// The result insert is the LAST write, after owner, receipt, terminal session and slot writes.
	rejectMatchResults(t, fixture)
	if _, err := handler.Handle(fixture.Ctx, command); err == nil {
		t.Fatal("injected result rejection unexpectedly committed")
	}
	if _, err := matches.FindResult(fixture.Ctx, command.Result.SessionID); !errors.Is(err, gameAbstract.ErrMatchResultNotFound) {
		t.Fatalf("result escaped rollback: %v", err)
	}
	stored, err := fixture.Repository.FindByID(fixture.Ctx, command.Result.SessionID)
	if err != nil || stored.Snapshot().State != gameDomain.SessionRunning {
		t.Fatalf("partial terminal state: %v", err)
	}
	if _, err := fixture.Repository.FindActive(fixture.Ctx, fixture.HouseID, houseRockets.GameKey); err != nil {
		t.Fatalf("slot escaped rollback: %v", err)
	}
	count, err := fixture.DB.Collection(entities.GameSessionCommandReceiptCollectionName).CountDocuments(fixture.Ctx, bson.M{"commandId": bson.M{"$in": bson.A{"complete:" + command.Result.SessionID, "cancel-match"}}})
	if err != nil || count != 0 {
		t.Fatalf("receipts escaped rollback: %d %v", count, err)
	}
	acceptMatchResults(t, fixture)
	result, err := handler.Handle(fixture.Ctx, command)
	if err != nil || result.Status != houseRockets.ResultCancelled || result.WinnerID != nil {
		t.Fatalf("retry: %+v %v", result, err)
	}
	for _, player := range result.Players {
		if player.Rank != nil {
			t.Fatal("cancelled match has rank")
		}
	}
	// The ordinary cancellation handler recognizes the transaction's user receipt.
	if _, err := gameCommands.NewCancelGameSessionHandler(fixture.Repository, fixture.MembershipPolicy(), gamekit.RocketsGames(), nil).Handle(fixture.Ctx, gameCommands.CancelGameSessionCommand{CommandID: "cancel-match", SessionID: result.SessionID, UserID: fixture.OwnerID}); err != nil {
		t.Fatalf("cancel retry not idempotent: %v", err)
	}
}

func TestHouseRocketsResultStaleOwnerCannotCommit(t *testing.T) {
	fixture, matches, command := completionFixture(t, houseRockets.EndSimultaneousElimination)
	owners := fixture.Repository.(gameAbstract.RuntimeOwnerRepository)
	current, err := owners.ClaimRuntimeOwner(fixture.Ctx, command.Owner.Generation, gameAbstract.RuntimeOwner{SessionID: command.Result.SessionID, OwnerInstanceID: "takeover", LeaseID: "takeover-lease"})
	if err != nil {
		t.Fatal(err)
	}
	handler := gamekit.NewRocketsCompletionHandler(fixture.Repository, matches)
	if _, err := handler.Handle(fixture.Ctx, command); !errors.Is(err, gameAbstract.ErrRuntimeOwnerConflict) {
		t.Fatalf("stale owner completed: %v", err)
	}
	if _, err := matches.FindResult(fixture.Ctx, command.Result.SessionID); !errors.Is(err, gameAbstract.ErrMatchResultNotFound) {
		t.Fatal("stale result persisted")
	}
	command.Owner = current
	result, err := handler.Handle(fixture.Ctx, command)
	if err != nil || result.WinnerID != nil || result.EndReason != houseRockets.EndSimultaneousElimination {
		t.Fatalf("current owner draw: %+v %v", result, err)
	}
}

func TestHouseRocketsResultTakeoverAndCompletionHaveOneWinner(t *testing.T) {
	for range 3 {
		fixture, matches, command := completionFixture(t, houseRockets.EndLastSurvivor)
		handler := gamekit.NewRocketsCompletionHandler(fixture.Repository, matches)
		start := make(chan struct{})
		completionErrors, claimErrors := make(chan error, 1), make(chan error, 1)
		go func() { <-start; _, err := handler.Handle(fixture.Ctx, command); completionErrors <- err }()
		go func() {
			<-start
			_, err := fixture.Repository.(gameAbstract.RuntimeOwnerRepository).ClaimRuntimeOwner(fixture.Ctx, command.Owner.Generation, gameAbstract.RuntimeOwner{SessionID: command.Result.SessionID, OwnerInstanceID: "racing-takeover", LeaseID: "racing-lease"})
			claimErrors <- err
		}()
		close(start)
		completionErr, claimErr := <-completionErrors, <-claimErrors
		if (completionErr == nil) == (claimErr == nil) {
			t.Fatalf("both/neither committed: finish=%v claim=%v", completionErr, claimErr)
		}
		if completionErr != nil && !errors.Is(completionErr, gameAbstract.ErrRuntimeOwnerConflict) {
			t.Fatal(completionErr)
		}
		if claimErr != nil && !errors.Is(claimErr, gameAbstract.ErrRuntimeOwnerConflict) {
			t.Fatal(claimErr)
		}
		_, resultErr := matches.FindResult(fixture.Ctx, command.Result.SessionID)
		if completionErr == nil && resultErr != nil {
			t.Fatal("winning completion lost result")
		}
		if completionErr != nil && !errors.Is(resultErr, gameAbstract.ErrMatchResultNotFound) {
			t.Fatal("losing completion wrote result")
		}
	}
}

func TestHouseRocketsGatewayRunningCancellationAndHTTPFallback(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := gamekit.PersistRocketsSession(t, fixture.GameSessionApplicationFixture, true)
	if status, _ := fixture.ResultHTTP(t, 0, session.SessionID, fixture.OwnerID); status != 404 {
		t.Fatalf("uncommitted result: %d", status)
	}
	if status, _ := fixture.ResultHTTP(t, 0, session.SessionID, fixture.OutsiderID); status != 403 {
		t.Fatalf("outsider uncommitted result: %d", status)
	}
	first := fixture.Connect(t, 0, session.SessionID, fixture.OwnerID)
	second := fixture.Connect(t, 1, session.SessionID, fixture.MemberID)
	_ = first.Grant(t)
	_ = second.Grant(t)
	if _, err := fixture.DB.Collection(entities.GameSessionCommandReceiptCollectionName).InsertOne(fixture.Ctx, entities.GameSessionCommandReceipt{ActorID: fixture.OwnerID, CommandID: "cancel-reused", CommandType: gameCommands.JoinGameSessionCommandType, PayloadHash: "another-command", SessionID: session.SessionID, ProcessedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	first.Send(t, gameCommands.CancelGameSessionCommandType, "cancel-reused", struct{}{})
	rejected := first.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "cancel-reused"
	})
	if rejected.Error == nil || rejected.Error.Code != "game.error.command_id_reused" {
		t.Fatalf("reused cancellation: %+v", rejected)
	}
	second.Send(t, gameCommands.CancelGameSessionCommandType, "unauthorized-cancel", struct{}{})
	second.Await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "unauthorized-cancel"
	})
	first.Send(t, gameCommands.CancelGameSessionCommandType, "authorized-cancel", struct{}{})
	appliedCancellation := false
	message := first.Await(t, func(message realtime.ServerMessage) bool {
		if message.Type == realtime.GameSessionSnapshotEventType && message.MessageID == "authorized-cancel" {
			appliedCancellation = true
		}
		return message.Type == houseRockets.ResultMessageType
	})
	if !appliedCancellation {
		t.Fatal("committed cancellation lost its command correlation")
	}
	var result houseRockets.HouseRocketsResultModel
	if err := json.Unmarshal(message.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != houseRockets.ResultCancelled || result.EndReason != houseRockets.EndCancelledByUser || result.WinnerID != nil {
		t.Fatalf("cancel result: %+v", result)
	}
	// Another device with no socket can recover the same immutable result.
	status, read := fixture.ResultHTTP(t, 1, session.SessionID, fixture.MemberID)
	if status != 200 || !bytes.Equal(message.Payload, gamekit.MustJSON(t, read)) {
		t.Fatalf("HTTP fallback: %d %+v", status, read)
	}
	if status, _ := fixture.ResultHTTP(t, 0, session.SessionID, ""); status != 401 {
		t.Fatalf("anonymous result: %d", status)
	}
	_, err := gameCommands.NewCancelGameSessionHandler(fixture.Repository, fixture.MembershipPolicy(), gamekit.RocketsGames(), nil).Handle(fixture.Ctx, gameCommands.CancelGameSessionCommand{CommandID: "authorized-cancel", SessionID: session.SessionID, UserID: fixture.OwnerID})
	if err != nil {
		t.Fatalf("committed cancel receipt not replayable: %v", err)
	}
}
