package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/data/entities"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const GameMatchResultCollectionName = "GameMatchResult"

type gameMatchResultDocument struct {
	SessionID       string                       `bson:"_id"`
	HouseID         string                       `bson:"houseId"`
	GameKey         string                       `bson:"gameKey"`
	SchemaVersion   int                          `bson:"schemaVersion"`
	Status          gameDomain.MatchResultStatus `bson:"status"`
	EndReason       string                       `bson:"endReason"`
	StartedAt       time.Time                    `bson:"startedAt"`
	EndedAt         time.Time                    `bson:"endedAt"`
	ResultEventType string                       `bson:"resultEventType"`
	RuntimeEpoch    int64                        `bson:"runtimeEpoch"`
	Payload         []byte                       `bson:"payload"`
	PayloadHash     string                       `bson:"payloadHash"`
	CreatedAt       time.Time                    `bson:"createdAt"`
}

func (document gameMatchResultDocument) result() gameDomain.MatchResult {
	return gameDomain.MatchResult{SessionID: document.SessionID, HouseID: document.HouseID, GameKey: document.GameKey, SchemaVersion: document.SchemaVersion, Status: document.Status, EndReason: document.EndReason, StartedAt: document.StartedAt, EndedAt: document.EndedAt, Payload: document.Payload}
}

type GameMatchRepository struct {
	sessions *GameSessionRepository
	results  *mongo.Collection
}

func NewGameMatchRepository(client *mongo.Client, dbName string) *GameMatchRepository {
	return &GameMatchRepository{NewGameSessionRepository(client, dbName), client.Database(dbName).Collection(GameMatchResultCollectionName)}
}

func (repository *GameMatchRepository) FindResult(ctx context.Context, sessionID string) (gameDomain.MatchResult, error) {
	var document gameMatchResultDocument
	err := repository.results.FindOne(ctx, bson.M{"_id": sessionID}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return gameDomain.MatchResult{}, gameAbstract.ErrMatchResultNotFound
	}
	if err != nil {
		return gameDomain.MatchResult{}, err
	}
	digest := sha256.Sum256(document.Payload)
	result := document.result()
	if hex.EncodeToString(digest[:]) != document.PayloadHash || document.RuntimeEpoch < 1 || strings.TrimSpace(document.ResultEventType) == "" || result.Validate() != nil {
		return gameDomain.MatchResult{}, gameAbstract.ErrInvalidPersistenceBatch
	}
	return result, nil
}

func (repository *GameMatchRepository) Complete(ctx context.Context, completion gameAbstract.MatchCompletion) (gameDomain.MatchResult, error) {
	expectedVersion, snapshot, events := completion.ExpectedVersion, completion.Session, completion.Events
	result, owner, command := completion.Result, completion.Owner, completion.Command
	if err := validatePersistenceBatch(expectedVersion, snapshot, events); err != nil {
		return gameDomain.MatchResult{}, err
	}
	if err := validateCommandDescriptor(command); err != nil {
		return gameDomain.MatchResult{}, err
	}
	if result.Validate() != nil || result.SessionID != snapshot.SessionID || result.HouseID != snapshot.HouseID || result.GameKey != snapshot.GameKey || owner.SessionID != result.SessionID || owner.Generation <= 0 || owner.OwnerInstanceID == "" || owner.LeaseID == "" || strings.TrimSpace(completion.ResultEventType) == "" || (result.Status == gameDomain.MatchCompleted && snapshot.State != gameDomain.SessionFinished) || (result.Status == gameDomain.MatchCancelled && snapshot.State != gameDomain.SessionCancelled) || snapshot.EndReason != result.EndReason || !snapshot.StartedAt.Truncate(time.Millisecond).Equal(result.StartedAt.Truncate(time.Millisecond)) || !snapshot.EndedAt.Truncate(time.Millisecond).Equal(result.EndedAt.Truncate(time.Millisecond)) {
		return gameDomain.MatchResult{}, gameAbstract.ErrInvalidPersistenceBatch
	}
	if completion.TriggerCommand != nil {
		if err := validateCommandDescriptor(*completion.TriggerCommand); err != nil {
			return gameDomain.MatchResult{}, err
		}
	}
	payload := []byte(result.Payload)
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	if hash != command.PayloadHash {
		return gameDomain.MatchResult{}, gameAbstract.ErrInvalidPersistenceBatch
	}
	messages, err := outboxMessages(events)
	if err != nil {
		return gameDomain.MatchResult{}, err
	}
	now := time.Now().UTC()
	// Terminal session events use the session version; the immutable match result
	// is a separate one-version aggregate for the unique outbox index.
	messages = append(messages, entities.OutboxMessage{EventID: matchResultEventID(completion.ResultEventType, result.SessionID), AggregateType: "gameMatch", AggregateID: result.SessionID, AggregateVersion: 1, EventType: completion.ResultEventType, Payload: payload, OccurredAt: result.EndedAt, CreatedAt: now, NextAttemptAt: now})
	document := gameMatchResultDocument{SessionID: result.SessionID, HouseID: result.HouseID, GameKey: result.GameKey, SchemaVersion: result.SchemaVersion, Status: result.Status, EndReason: result.EndReason, StartedAt: result.StartedAt, EndedAt: result.EndedAt, ResultEventType: completion.ResultEventType, RuntimeEpoch: owner.Generation, Payload: payload, PayloadHash: hash, CreatedAt: now}
	err = repository.sessions.sessions.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		var existing gameMatchResultDocument
		err := repository.results.FindOne(txCtx, bson.M{"_id": result.SessionID}).Decode(&existing)
		if err == nil {
			if !sameMatchResult(existing, document) {
				return gameAbstract.ErrMatchResultConflict
			}
			return nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		if len(events) == 0 {
			return gameAbstract.ErrInvalidPersistenceBatch
		}
		// A conditional write fences concurrent ownership claims: a stale owner
		// cannot commit a result based only on a transaction's snapshot read.
		fence, err := repository.results.Database().Collection(RuntimeOwnerCollectionName).UpdateOne(txCtx, bson.M{"_id": owner.SessionID, "generation": owner.Generation, "ownerInstanceId": owner.OwnerInstanceID, "leaseId": owner.LeaseID, "started": true, "completed": bson.M{"$ne": true}}, bson.M{"$set": bson.M{"completed": true}})
		if err != nil {
			return err
		}
		if fence.MatchedCount != 1 {
			return gameAbstract.ErrRuntimeOwnerConflict
		}
		if _, err := repository.sessions.receipts.Insert(txCtx, commandReceipt(command, result.SessionID)); err != nil {
			return err
		}
		if completion.TriggerCommand != nil {
			if _, err := repository.sessions.receipts.Insert(txCtx, commandReceipt(*completion.TriggerCommand, result.SessionID)); err != nil {
				return err
			}
		}
		updated, err := repository.sessions.sessions.Collection().ReplaceOne(txCtx, bson.M{"_id": result.SessionID, "version": expectedVersion, "state": gameDomain.SessionRunning}, gameSessionDocumentFromSnapshot(snapshot))
		if err != nil {
			return err
		}
		if updated.MatchedCount != 1 {
			return gameAbstract.ErrConcurrentSessionUpdate
		}
		removed, err := repository.sessions.activeSessions.Collection().DeleteOne(txCtx, bson.M{"houseId": result.HouseID, "gameKey": result.GameKey, "sessionId": result.SessionID})
		if err != nil {
			return err
		}
		if removed.DeletedCount != 1 {
			return gameAbstract.ErrActiveSessionInvariant
		}
		if _, err := repository.results.InsertOne(txCtx, document); err != nil {
			return err
		}
		return repository.sessions.outbox.InsertMany(txCtx, messages)
	})
	if err != nil {
		// An ambiguous commit response must resolve to canonical stored data;
		// never release a newer session's slot or manufacture a second result.
		var existing gameMatchResultDocument
		if lookupErr := repository.results.FindOne(ctx, bson.M{"_id": result.SessionID}).Decode(&existing); lookupErr == nil {
			if !sameMatchResult(existing, document) {
				return gameDomain.MatchResult{}, gameAbstract.ErrMatchResultConflict
			}
			err = nil
		}
	}
	if err != nil {
		return gameDomain.MatchResult{}, err
	}
	return repository.FindResult(ctx, result.SessionID)
}

func sameMatchResult(first, second gameMatchResultDocument) bool {
	return first.SessionID == second.SessionID && first.HouseID == second.HouseID && first.GameKey == second.GameKey && first.SchemaVersion == second.SchemaVersion && first.Status == second.Status && first.EndReason == second.EndReason && first.StartedAt.Truncate(time.Millisecond).Equal(second.StartedAt.Truncate(time.Millisecond)) && first.EndedAt.Truncate(time.Millisecond).Equal(second.EndedAt.Truncate(time.Millisecond)) && first.ResultEventType == second.ResultEventType && first.PayloadHash == second.PayloadHash
}

func matchResultEventID(eventType, sessionID string) string { return eventType + ":" + sessionID }

func (repository *GameMatchRepository) MarkPublished(ctx context.Context, sessionID string) error {
	var document gameMatchResultDocument
	if err := repository.results.FindOne(ctx, bson.M{"_id": sessionID}).Decode(&document); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return gameAbstract.ErrMatchResultNotFound
		}
		return err
	}
	_, err := repository.sessions.outbox.Collection().UpdateOne(ctx, bson.M{"eventId": matchResultEventID(document.ResultEventType, sessionID), "publishedAt": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"publishedAt": time.Now().UTC()}})
	return err
}

var _ gameAbstract.GameMatchRepository = (*GameMatchRepository)(nil)
