package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

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
	"houseflowApi/internal/data/migrations"
	infrastructureCoordination "houseflowApi/internal/infrastructure/coordination"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/models/core"
)

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestHouseRocketsGatewayKeepsFinalizingDuringTransactionFailure(t *testing.T) {
	fixture := newRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := persistRocketsSession(t, fixture.gameSessionApplicationFixture, true)
	first := fixture.connect(t, 0, session.SessionID, fixture.ownerID)
	second := fixture.connect(t, 1, session.SessionID, fixture.memberID)
	_ = first.grant(t)
	_ = second.grant(t)
	if err := fixture.db.RunCommand(fixture.ctx, bson.D{{Key: "collMod", Value: entities.OutboxMessageCollectionName}, {Key: "validator", Value: bson.M{"eventType": bson.M{"$ne": houseRockets.ResultMessageType}}}}).Err(); err != nil {
		t.Fatal(err)
	}
	first.send(t, gameCommands.CancelGameSessionCommandType, "pending-cancel", struct{}{})
	finalizing := second.snapshot(t, func(model houseRockets.HouseRocketsSnapshotModel) bool {
		return model.Phase == houseRockets.PhaseFinalizing
	})
	if finalizing.WinnerID != nil {
		t.Fatal("winner published before commit")
	}
	// Retry of the same cancel is held until the shared transaction commits.
	first.send(t, gameCommands.CancelGameSessionCommandType, "pending-cancel", struct{}{})
	deadline := time.NewTimer(800 * time.Millisecond)
	defer deadline.Stop()
waitForRetry:
	for {
		select {
		case message := <-second.messages:
			if message.Type == houseRockets.ResultMessageType {
				t.Fatal("result event escaped rejected transaction")
			}
		case <-deadline.C:
			break waitForRetry
		}
	}
	if status, _ := fixture.resultHTTP(t, 1, session.SessionID, fixture.memberID); status != 404 {
		t.Fatalf("uncommitted HTTP result: %d", status)
	}
	active, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, houseRockets.GameKey)
	if err != nil || active.Snapshot().State != gameDomain.SessionRunning {
		t.Fatalf("active slot released before commit: %v", err)
	}
	if err := fixture.db.RunCommand(fixture.ctx, bson.D{{Key: "collMod", Value: entities.OutboxMessageCollectionName}, {Key: "validator", Value: bson.M{}}}).Err(); err != nil {
		t.Fatal(err)
	}
	second.await(t, func(message realtime.ServerMessage) bool { return message.Type == houseRockets.ResultMessageType })
	if status, _ := fixture.resultHTTP(t, 1, session.SessionID, fixture.memberID); status != 200 {
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

func TestHouseRocketsResultSurvivesPostCommitPublishFailure(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	session := persistRocketsSession(t, fixture, true)
	matches := database.NewGameMatchRepository(fixture.db.Client(), fixture.db.Name())
	mediator := newGameSessionMediator(fixture)
	cqrs.MustRegister[houseRockets.HouseRocketsResultModel, rocketsCommands.CompleteMatchCommand](mediator, rocketsCommands.NewCompleteMatchHandler(fixture.repository, matches))
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.AuthorizeCancellationQuery](mediator, gameQueries.NewAuthorizeCancellationHandler(fixture.repository, fixture.membershipPolicy()))
	coordinator := resultPublishFailureCoordinator{newTestCoordinator(t, prepareRedis(t), "result-publish-failure")}
	manager, err := realtime.NewRoomManager(coordinator, fixture.repository, mediator, realtime.RoomManagerOptions{MatchRepository: matches, RenewInterval: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Start(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRoomManager(t, manager) })
	if _, err = manager.EnsureRoom(fixture.ctx, session.SessionID); err != nil {
		t.Fatal(err)
	}
	if err = manager.Dispatch(fixture.ctx, coordinationAbstract.MessageEnvelope{MessageID: "cancel-before-publish-failure", RoomID: session.SessionID, ActorID: fixture.ownerID, Type: gameCommands.CancelGameSessionCommandType}); err != nil {
		t.Fatal(err)
	}
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case failure := <-manager.Errors():
			if errors.Is(failure.Err, coordinationAbstract.ErrUnavailable) {
				result, err := matches.FindResult(fixture.ctx, session.SessionID)
				if err != nil || result.Status != gameDomain.MatchCancelled {
					t.Fatalf("result lost after commit: %+v %v", result, err)
				}
				if _, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, houseRockets.GameKey); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
					t.Fatalf("committed slot retained: %v", err)
				}
				var outbox entities.OutboxMessage
				if err := fixture.db.Collection(entities.OutboxMessageCollectionName).FindOne(fixture.ctx, bson.M{"eventId": "houseRockets.result:" + session.SessionID}).Decode(&outbox); err != nil || outbox.PublishedAt != nil {
					t.Fatalf("undelivered outbox marked published: %+v %v", outbox, err)
				}
				return
			}
		case <-timeout.C:
			t.Fatal("publish failure was not observed")
		}
	}
}

func TestHouseRocketsResultMigrationIsRepeatableAndRejectsInvalidDocuments(t *testing.T) {
	fixture := newGameSessionPersistenceFixture(t)
	for _, migration := range migrations.AllMigrations() {
		if migration.Version() == "0042" {
			for range 2 {
				if err := migration.Up(fixture.ctx, fixture.db); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if _, err := fixture.db.Collection(database.GameMatchResultCollectionName).InsertOne(fixture.ctx, bson.M{"_id": "invalid", "runtimeEpoch": int64(0)}); err == nil {
		t.Fatal("invalid result passed schema validator")
	}
	count, err := fixture.db.Collection("localization").CountDocuments(fixture.ctx, bson.M{"key": "houseRockets.error.result_not_found"})
	if err != nil || count != 2 {
		t.Fatalf("localization duplication: %d %v", count, err)
	}
}

func (fixture *rocketsGatewayFixture) resultHTTP(t *testing.T, index int, sessionID, userID string) (int, houseRockets.HouseRocketsResultModel) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, fixture.urls[index]+"/api/v1/game/"+sessionID+"/result", nil)
	if userID != "" {
		request.Header.Set("Authorization", "Bearer "+generateGatewayToken(t, fixture.jwt, userID))
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body core.ApiResponse[houseRockets.HouseRocketsResultModel]
	if response.StatusCode == 200 {
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Success {
			t.Fatal("unsuccessful result response")
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	return response.StatusCode, body.Data
}

func completionFixture(t *testing.T, reason houseRockets.EndReason) (*gameSessionApplicationFixture, *database.GameMatchRepository, rocketsCommands.CompleteMatchCommand) {
	t.Helper()
	fixture := newGameSessionApplicationFixture(t)
	snapshot := persistRocketsSession(t, fixture, true)
	stored, err := fixture.repository.FindByID(fixture.ctx, snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot = stored.Snapshot()
	owners := fixture.repository.(gameAbstract.RuntimeOwnerRepository)
	owner, err := owners.ClaimRuntimeOwner(fixture.ctx, 0, gameAbstract.RuntimeOwner{SessionID: snapshot.SessionID, OwnerInstanceID: "result-owner", LeaseID: "result-lease"})
	if err != nil {
		t.Fatal(err)
	}
	if err := owners.MarkRuntimeStarted(fixture.ctx, owner); err != nil {
		t.Fatal(err)
	}
	simulation, err := houseRockets.NewSimulation(houseRockets.NewSimulationParams{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, StartedAt: snapshot.StartedAt, Players: []houseRockets.PlayerIdentity{{PlayerID: fixture.ownerID, DisplayName: "Owner"}, {PlayerID: fixture.memberID, DisplayName: "Member"}}})
	if err != nil {
		t.Fatal(err)
	}
	switch reason {
	case houseRockets.EndLastSurvivor:
		err = simulation.EliminatePlayers([]string{fixture.ownerID}, houseRockets.EliminationForfeit)
	case houseRockets.EndSimultaneousElimination:
		err = simulation.EliminatePlayers([]string{fixture.ownerID, fixture.memberID}, houseRockets.EliminationConnectionExpired)
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
	return fixture, database.NewGameMatchRepository(fixture.db.Client(), fixture.db.Name()), rocketsCommands.CompleteMatchCommand{Result: result, Owner: owner}
}

func TestHouseRocketsResultFiveMinuteExpiryIsPersistedWithoutWinner(t *testing.T) {
	fixture, matches, command := completionFixture(t, houseRockets.EndSessionExpired)
	result, err := rocketsCommands.NewCompleteMatchHandler(fixture.repository, matches).Handle(fixture.ctx, command)
	if err != nil || result.Status != houseRockets.ResultCancelled || result.DurationSeconds != 300 || result.WinnerID != nil {
		t.Fatalf("expiry result: %+v %v", result, err)
	}
	for _, player := range result.Players {
		if player.Rank != nil {
			t.Fatal("expiry produced leaderboard rank")
		}
	}
	stored, err := fixture.repository.FindByID(fixture.ctx, result.SessionID)
	if err != nil || stored.Snapshot().State != gameDomain.SessionCancelled {
		t.Fatalf("expiry session: %v", err)
	}
}

func TestHouseRocketsGatewayPregameCancellationDoesNotCreateResult(t *testing.T) {
	fixture := newRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := fixture.ensureHTTP(t)
	first := fixture.connect(t, 0, session.SessionId, fixture.ownerID)
	first.lifecycle(t, gameCommands.CancelGameSessionCommandType, "pregame-cancel", struct{}{})
	if status, _ := fixture.resultHTTP(t, 1, session.SessionId, fixture.memberID); status != 404 {
		t.Fatalf("pregame result: %d", status)
	}
	rematch := fixture.ensureHTTP(t)
	if rematch.SessionId == session.SessionId {
		t.Fatal("pregame cancellation retained active slot")
	}
}

func TestHouseRocketsResultConcurrentCompletionIsImmutableAndRematchSafe(t *testing.T) {
	fixture, matches, command := completionFixture(t, houseRockets.EndLastSurvivor)
	handler := rocketsCommands.NewCompleteMatchHandler(fixture.repository, matches)
	start := make(chan struct{})
	var workers sync.WaitGroup
	errorsChannel := make(chan error, 4)
	results := make(chan houseRockets.HouseRocketsResultModel, 4)
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result, err := handler.Handle(fixture.ctx, command)
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
		if !bytes.Equal(mustJSON(t, result), mustJSON(t, command.Result)) {
			t.Fatalf("canonical result changed: %+v", result)
		}
	}
	for _, collection := range []string{database.GameMatchResultCollectionName, entities.GameSessionCommandReceiptCollectionName, entities.OutboxMessageCollectionName} {
		filter := bson.M{"_id": command.Result.SessionID}
		if collection == entities.GameSessionCommandReceiptCollectionName {
			filter = bson.M{"commandId": "complete:" + command.Result.SessionID}
		}
		if collection == entities.OutboxMessageCollectionName {
			filter = bson.M{"eventType": houseRockets.ResultMessageType, "aggregateId": command.Result.SessionID}
		}
		count, err := fixture.db.Collection(collection).CountDocuments(fixture.ctx, filter)
		if err != nil || count != 1 {
			t.Fatalf("%s count=%d: %v", collection, count, err)
		}
	}
	stored, err := fixture.repository.FindByID(fixture.ctx, command.Result.SessionID)
	if err != nil || stored.Snapshot().State != gameDomain.SessionFinished {
		t.Fatalf("terminal session: %v", err)
	}
	if _, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, houseRockets.GameKey); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("active slot retained: %v", err)
	}
	owners := fixture.repository.(gameAbstract.RuntimeOwnerRepository)
	if _, err := owners.ClaimRuntimeOwner(fixture.ctx, command.Owner.Generation, gameAbstract.RuntimeOwner{SessionID: command.Result.SessionID, OwnerInstanceID: "new-owner", LeaseID: "new-lease"}); !errors.Is(err, gameAbstract.ErrRuntimeOwnerConflict) {
		t.Fatalf("completed owner reclaimed: %v", err)
	}
	// Create another lobby in the same slot; replaying old completion cannot delete it.
	newSession, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "rematch-session", HouseID: fixture.houseID, GameKey: houseRockets.GameKey, ProtocolVersion: 2, Mode: gameDomain.RealtimeGame, Rules: houseRockets.Definition().Rules, CreatedBy: fixture.ownerID, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.Create(fixture.ctx, newSession.Snapshot(), newSession.PendingEvents(), persistenceCommand("rematch-create", "create", "new-lobby")); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Handle(fixture.ctx, command); err != nil {
		t.Fatal(err)
	}
	active, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, houseRockets.GameKey)
	if err != nil || active.Snapshot().SessionID != "rematch-session" {
		t.Fatalf("old completion deleted new slot: %v", err)
	}
	command.Result.Players[0].Distance = 1
	if _, err := handler.Handle(fixture.ctx, command); err == nil {
		t.Fatal("different result overwrote canonical result")
	}
}

func TestHouseRocketsResultTransactionRollbackAndRetry(t *testing.T) {
	fixture, matches, command := completionFixture(t, houseRockets.EndCancelledByUser)
	command.CancelCommandID, command.CancelUserID = "cancel-match", fixture.ownerID
	handler := rocketsCommands.NewCompleteMatchHandler(fixture.repository, matches)
	// Fail the LAST write after owner, receipts, terminal session, slot and result writes.
	if err := fixture.db.RunCommand(fixture.ctx, bson.D{{Key: "collMod", Value: entities.OutboxMessageCollectionName}, {Key: "validator", Value: bson.M{"eventType": bson.M{"$ne": houseRockets.ResultMessageType}}}, {Key: "validationLevel", Value: "strict"}, {Key: "validationAction", Value: "error"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Handle(fixture.ctx, command); err == nil {
		t.Fatal("injected outbox rejection unexpectedly committed")
	}
	if _, err := matches.FindResult(fixture.ctx, command.Result.SessionID); !errors.Is(err, gameAbstract.ErrMatchResultNotFound) {
		t.Fatalf("result escaped rollback: %v", err)
	}
	stored, err := fixture.repository.FindByID(fixture.ctx, command.Result.SessionID)
	if err != nil || stored.Snapshot().State != gameDomain.SessionRunning {
		t.Fatalf("partial terminal state: %v", err)
	}
	if _, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, houseRockets.GameKey); err != nil {
		t.Fatalf("slot escaped rollback: %v", err)
	}
	count, err := fixture.db.Collection(entities.GameSessionCommandReceiptCollectionName).CountDocuments(fixture.ctx, bson.M{"commandId": bson.M{"$in": bson.A{"complete:" + command.Result.SessionID, "cancel-match"}}})
	if err != nil || count != 0 {
		t.Fatalf("receipts escaped rollback: %d %v", count, err)
	}
	if err := fixture.db.RunCommand(fixture.ctx, bson.D{{Key: "collMod", Value: entities.OutboxMessageCollectionName}, {Key: "validator", Value: bson.M{}}}).Err(); err != nil {
		t.Fatal(err)
	}
	result, err := handler.Handle(fixture.ctx, command)
	if err != nil || result.Status != houseRockets.ResultCancelled || result.WinnerID != nil {
		t.Fatalf("retry: %+v %v", result, err)
	}
	for _, player := range result.Players {
		if player.Rank != nil {
			t.Fatal("cancelled match has rank")
		}
	}
	// The ordinary cancellation handler recognizes the transaction's user receipt.
	if _, err := gameCommands.NewCancelGameSessionHandler(fixture.repository, fixture.membershipPolicy()).Handle(fixture.ctx, gameCommands.CancelGameSessionCommand{CommandID: "cancel-match", SessionID: result.SessionID, UserID: fixture.ownerID}); err != nil {
		t.Fatalf("cancel retry not idempotent: %v", err)
	}
}

func TestHouseRocketsResultStaleOwnerCannotCommit(t *testing.T) {
	fixture, matches, command := completionFixture(t, houseRockets.EndSimultaneousElimination)
	owners := fixture.repository.(gameAbstract.RuntimeOwnerRepository)
	current, err := owners.ClaimRuntimeOwner(fixture.ctx, command.Owner.Generation, gameAbstract.RuntimeOwner{SessionID: command.Result.SessionID, OwnerInstanceID: "takeover", LeaseID: "takeover-lease"})
	if err != nil {
		t.Fatal(err)
	}
	handler := rocketsCommands.NewCompleteMatchHandler(fixture.repository, matches)
	if _, err := handler.Handle(fixture.ctx, command); !errors.Is(err, gameAbstract.ErrRuntimeOwnerConflict) {
		t.Fatalf("stale owner completed: %v", err)
	}
	if _, err := matches.FindResult(fixture.ctx, command.Result.SessionID); !errors.Is(err, gameAbstract.ErrMatchResultNotFound) {
		t.Fatal("stale result persisted")
	}
	command.Owner = current
	result, err := handler.Handle(fixture.ctx, command)
	if err != nil || result.WinnerID != nil || result.EndReason != houseRockets.EndSimultaneousElimination {
		t.Fatalf("current owner draw: %+v %v", result, err)
	}
}

func TestHouseRocketsResultTakeoverAndCompletionHaveOneWinner(t *testing.T) {
	for range 3 {
		fixture, matches, command := completionFixture(t, houseRockets.EndLastSurvivor)
		handler := rocketsCommands.NewCompleteMatchHandler(fixture.repository, matches)
		start := make(chan struct{})
		completionErrors, claimErrors := make(chan error, 1), make(chan error, 1)
		go func() { <-start; _, err := handler.Handle(fixture.ctx, command); completionErrors <- err }()
		go func() {
			<-start
			_, err := fixture.repository.(gameAbstract.RuntimeOwnerRepository).ClaimRuntimeOwner(fixture.ctx, command.Owner.Generation, gameAbstract.RuntimeOwner{SessionID: command.Result.SessionID, OwnerInstanceID: "racing-takeover", LeaseID: "racing-lease"})
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
		_, resultErr := matches.FindResult(fixture.ctx, command.Result.SessionID)
		if completionErr == nil && resultErr != nil {
			t.Fatal("winning completion lost result")
		}
		if completionErr != nil && !errors.Is(resultErr, gameAbstract.ErrMatchResultNotFound) {
			t.Fatal("losing completion wrote result")
		}
	}
}

func TestHouseRocketsGatewayRunningCancellationAndHTTPFallback(t *testing.T) {
	fixture := newRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := persistRocketsSession(t, fixture.gameSessionApplicationFixture, true)
	if status, _ := fixture.resultHTTP(t, 0, session.SessionID, fixture.ownerID); status != 404 {
		t.Fatalf("uncommitted result: %d", status)
	}
	if status, _ := fixture.resultHTTP(t, 0, session.SessionID, fixture.outsiderID); status != 403 {
		t.Fatalf("outsider uncommitted result: %d", status)
	}
	first := fixture.connect(t, 0, session.SessionID, fixture.ownerID)
	second := fixture.connect(t, 1, session.SessionID, fixture.memberID)
	_ = first.grant(t)
	_ = second.grant(t)
	if _, err := fixture.db.Collection(entities.GameSessionCommandReceiptCollectionName).InsertOne(fixture.ctx, entities.GameSessionCommandReceipt{ActorID: fixture.ownerID, CommandID: "cancel-reused", CommandType: gameCommands.JoinGameSessionCommandType, PayloadHash: "another-command", SessionID: session.SessionID, ProcessedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	first.send(t, gameCommands.CancelGameSessionCommandType, "cancel-reused", struct{}{})
	rejected := first.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "cancel-reused"
	})
	if rejected.Error == nil || rejected.Error.Code != "game.error.command_id_reused" {
		t.Fatalf("reused cancellation: %+v", rejected)
	}
	second.send(t, gameCommands.CancelGameSessionCommandType, "unauthorized-cancel", struct{}{})
	second.await(t, func(message realtime.ServerMessage) bool {
		return message.Type == realtime.CommandRejectedMessageType && message.MessageID == "unauthorized-cancel"
	})
	first.send(t, gameCommands.CancelGameSessionCommandType, "authorized-cancel", struct{}{})
	appliedCancellation := false
	message := first.await(t, func(message realtime.ServerMessage) bool {
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
	status, read := fixture.resultHTTP(t, 1, session.SessionID, fixture.memberID)
	if status != 200 || !bytes.Equal(message.Payload, mustJSON(t, read)) {
		t.Fatalf("HTTP fallback: %d %+v", status, read)
	}
	if status, _ := fixture.resultHTTP(t, 0, session.SessionID, ""); status != 401 {
		t.Fatalf("anonymous result: %d", status)
	}
	_, err := gameCommands.NewCancelGameSessionHandler(fixture.repository, fixture.membershipPolicy()).Handle(fixture.ctx, gameCommands.CancelGameSessionCommand{CommandID: "authorized-cancel", SessionID: session.SessionID, UserID: fixture.ownerID})
	if err != nil {
		t.Fatalf("committed cancel receipt not replayable: %v", err)
	}
}
