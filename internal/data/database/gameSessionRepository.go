package database

import (
	"context"
	"errors"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// The runtime owner record only fences a live room; it is kept a while after the
// session ends so a delayed claimant still sees the completed generation.
const runtimeOwnerRetention = time.Hour

type GameSessionRepository struct {
	sessions databaseAbstract.DbRepository[gameSessionDocument]
	receipts databaseAbstract.DbRepository[entities.GameSessionCommandReceipt]
}

var _ gameAbstract.GameSessionRepository = (*GameSessionRepository)(nil)

func NewGameSessionRepository(client *mongo.Client, dbName string) *GameSessionRepository {
	return &GameSessionRepository{
		sessions: NewDbContext[gameSessionDocument](client, dbName),
		receipts: NewDbContext[entities.GameSessionCommandReceipt](client, dbName),
	}
}

func (repository *GameSessionRepository) Create(
	ctx context.Context,
	snapshot gameDomain.SessionSnapshot,
	events []gameDomain.SessionEvent,
	command gameAbstract.CommandDescriptor,
) (gameAbstract.PersistenceResult, error) {
	if err := validatePersistenceBatch(0, snapshot, events); err != nil {
		return gameAbstract.PersistenceResult{}, err
	}
	if err := validateCommandDescriptor(command); err != nil {
		return gameAbstract.PersistenceResult{}, err
	}
	document := gameSessionDocumentFromSnapshot(snapshot)

	// The unique activeKey index rejects a second active session for the slot.
	err := repository.sessions.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		if err := repository.insertReceipt(txCtx, command, snapshot.SessionID); err != nil {
			return err
		}
		_, err := repository.sessions.Insert(txCtx, document)
		return err
	})
	if mongo.IsDuplicateKeyError(err) {
		result, found, lookupErr := repository.FindProcessedCommand(ctx, command)
		if lookupErr != nil {
			return gameAbstract.PersistenceResult{}, lookupErr
		}
		if found {
			return result, nil
		}
		active, activeErr := repository.FindActive(ctx, snapshot.HouseID, snapshot.GameKey)
		if activeErr == nil && active.Snapshot().SessionID != snapshot.SessionID {
			return gameAbstract.PersistenceResult{}, gameAbstract.ErrActiveGameSessionExists
		}
		if activeErr != nil && !errors.Is(activeErr, gameAbstract.ErrGameSessionNotFound) {
			return gameAbstract.PersistenceResult{}, activeErr
		}
		return gameAbstract.PersistenceResult{}, gameAbstract.ErrGameSessionAlreadyExists
	}
	if err != nil {
		return gameAbstract.PersistenceResult{}, err
	}
	return gameAbstract.PersistenceResult{SessionID: snapshot.SessionID}, nil
}

func (repository *GameSessionRepository) FindByID(
	ctx context.Context,
	sessionID string,
) (*gameDomain.GameSession, error) {
	if sessionID == "" {
		return nil, gameDomain.ErrSessionIDRequired
	}
	var document gameSessionDocument
	err := repository.sessions.Collection().FindOne(ctx, bson.M{"_id": sessionID}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, gameAbstract.ErrGameSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	session, err := gameDomain.RestoreGameSession(document.snapshot())
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (repository *GameSessionRepository) FindActive(
	ctx context.Context,
	houseID string,
	gameKey string,
) (*gameDomain.GameSession, error) {
	if houseID == "" {
		return nil, gameDomain.ErrHouseIDRequired
	}
	if gameKey == "" {
		return nil, gameDomain.ErrGameKeyRequired
	}
	var document gameSessionDocument
	err := repository.sessions.Collection().FindOne(ctx, bson.M{"activeKey": activeSessionKey(houseID, gameKey)}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, gameAbstract.ErrGameSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	session, err := gameDomain.RestoreGameSession(document.snapshot())
	if err != nil {
		return nil, err
	}
	return validActiveSession(session, houseID, gameKey)
}

func validActiveSession(session *gameDomain.GameSession, houseID, gameKey string) (*gameDomain.GameSession, error) {
	snapshot := session.Snapshot()
	if snapshot.HouseID != houseID || snapshot.GameKey != gameKey || isTerminalSession(snapshot.State) {
		return nil, gameAbstract.ErrActiveSessionInvariant
	}
	return session, nil
}

func (repository *GameSessionRepository) FindProcessedCommand(
	ctx context.Context,
	command gameAbstract.CommandDescriptor,
) (gameAbstract.PersistenceResult, bool, error) {
	if err := validateCommandDescriptor(command); err != nil {
		return gameAbstract.PersistenceResult{}, false, err
	}
	if command.IsSystem() {
		return gameAbstract.PersistenceResult{}, false, nil
	}
	var receipt entities.GameSessionCommandReceipt
	err := repository.receipts.Collection().FindOne(ctx, bson.M{
		"actorId":   command.ActorID,
		"commandId": command.CommandID,
	}).Decode(&receipt)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return gameAbstract.PersistenceResult{}, false, nil
	}
	if err != nil {
		return gameAbstract.PersistenceResult{}, false, err
	}
	if receipt.CommandType != command.CommandType || receipt.PayloadHash != command.PayloadHash {
		return gameAbstract.PersistenceResult{}, false, gameAbstract.ErrCommandIDReused
	}
	return gameAbstract.PersistenceResult{
		SessionID:        receipt.SessionID,
		AlreadyProcessed: true,
	}, true, nil
}

func (repository *GameSessionRepository) Save(
	ctx context.Context,
	expectedVersion int64,
	snapshot gameDomain.SessionSnapshot,
	events []gameDomain.SessionEvent,
	command gameAbstract.CommandDescriptor,
) (gameAbstract.PersistenceResult, error) {
	if err := validatePersistenceBatch(expectedVersion, snapshot, events); err != nil {
		return gameAbstract.PersistenceResult{}, err
	}
	if err := validateCommandDescriptor(command); err != nil {
		return gameAbstract.PersistenceResult{}, err
	}
	if command.IsSystem() && len(events) == 0 {
		return gameAbstract.PersistenceResult{SessionID: snapshot.SessionID}, nil
	}
	document := gameSessionDocumentFromSnapshot(snapshot)
	// A non-terminal system transition is a single conditional write.
	if command.IsSystem() && !isTerminalSession(snapshot.State) {
		if err := repository.replaceSession(ctx, expectedVersion, document); err != nil {
			return gameAbstract.PersistenceResult{}, err
		}
		return gameAbstract.PersistenceResult{SessionID: snapshot.SessionID}, nil
	}

	err := repository.sessions.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		if err := repository.insertReceipt(txCtx, command, snapshot.SessionID); err != nil {
			return err
		}
		if err := repository.replaceSession(txCtx, expectedVersion, document); err != nil {
			return err
		}
		// The terminal snapshot carries no activeKey, which releases the slot.
		if !isTerminalSession(snapshot.State) {
			return nil
		}
		_, err := repository.runtimeOwners().UpdateOne(txCtx, bson.M{"_id": snapshot.SessionID}, bson.M{"$set": bson.M{"expiresAt": runtimeOwnerExpiry(snapshot)}})
		return err
	})
	if err != nil {
		result, found, lookupErr := repository.FindProcessedCommand(ctx, command)
		if lookupErr != nil {
			return gameAbstract.PersistenceResult{}, lookupErr
		}
		if found {
			return result, nil
		}
		return gameAbstract.PersistenceResult{}, err
	}
	return gameAbstract.PersistenceResult{SessionID: snapshot.SessionID}, nil
}

// replaceSession is the optimistic-concurrency write of a session snapshot.
func (repository *GameSessionRepository) replaceSession(ctx context.Context, expectedVersion int64, document gameSessionDocument) error {
	result, err := repository.sessions.Collection().ReplaceOne(ctx, bson.M{
		"_id":     document.SessionID,
		"version": expectedVersion,
	}, document)
	if err != nil {
		return err
	}
	if result.MatchedCount > 0 {
		return nil
	}
	exists, err := repository.sessions.ExistsByFilter(ctx, bson.M{"_id": document.SessionID})
	if err != nil {
		return err
	}
	if !exists {
		return gameAbstract.ErrGameSessionNotFound
	}
	return gameAbstract.ErrConcurrentSessionUpdate
}

func isTerminalSession(state gameDomain.SessionState) bool {
	return state == gameDomain.SessionFinished || state == gameDomain.SessionCancelled
}

func validateCommandDescriptor(command gameAbstract.CommandDescriptor) error {
	switch {
	case command.CommandID == "":
		return gameAbstract.ErrCommandIDRequired
	case command.ActorID == "":
		return gameAbstract.ErrCommandActorRequired
	case command.CommandType == "":
		return gameAbstract.ErrCommandTypeRequired
	case command.PayloadHash == "":
		return gameAbstract.ErrCommandPayloadRequired
	default:
		return nil
	}
}

// insertReceipt records a client command for idempotent replay. System
// transitions are fenced by the expected version and keep no receipt.
func (repository *GameSessionRepository) insertReceipt(
	ctx context.Context,
	command gameAbstract.CommandDescriptor,
	sessionID string,
) error {
	if command.IsSystem() {
		return nil
	}
	_, err := repository.receipts.Insert(ctx, commandReceipt(command, sessionID))
	return err
}

func runtimeOwnerExpiry(snapshot gameDomain.SessionSnapshot) time.Time {
	return snapshot.EndedAt.Add(runtimeOwnerRetention).UTC()
}

func commandReceipt(
	command gameAbstract.CommandDescriptor,
	sessionID string,
) entities.GameSessionCommandReceipt {
	return entities.GameSessionCommandReceipt{
		ActorID:     command.ActorID,
		CommandID:   command.CommandID,
		CommandType: command.CommandType,
		PayloadHash: command.PayloadHash,
		SessionID:   sessionID,
		ProcessedAt: time.Now().UTC(),
	}
}

func validatePersistenceBatch(
	expectedVersion int64,
	snapshot gameDomain.SessionSnapshot,
	events []gameDomain.SessionEvent,
) error {
	if expectedVersion < 0 || snapshot.Version < expectedVersion {
		return gameAbstract.ErrInvalidPersistenceBatch
	}
	if _, err := gameDomain.RestoreGameSession(snapshot); err != nil {
		return errors.Join(gameAbstract.ErrInvalidPersistenceBatch, err)
	}
	if len(events) == 0 {
		if snapshot.Version != expectedVersion {
			return gameAbstract.ErrInvalidPersistenceBatch
		}
		return nil
	}
	if snapshot.Version == expectedVersion {
		return gameAbstract.ErrInvalidPersistenceBatch
	}
	if int64(len(events)) != snapshot.Version-expectedVersion {
		return gameAbstract.ErrInvalidPersistenceBatch
	}

	eventIDs := make(map[string]struct{}, len(events))
	for index, event := range events {
		expectedEventVersion := expectedVersion + int64(index) + 1
		if event.EventID == "" ||
			event.SessionID != snapshot.SessionID ||
			event.Type == "" ||
			event.OccurredAt.IsZero() ||
			event.Version != expectedEventVersion {
			return gameAbstract.ErrInvalidPersistenceBatch
		}
		if _, exists := eventIDs[event.EventID]; exists {
			return gameAbstract.ErrInvalidPersistenceBatch
		}
		eventIDs[event.EventID] = struct{}{}
	}
	return nil
}
