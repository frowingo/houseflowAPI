package database

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const gameSessionAggregateType = "gameSession"

type GameSessionRepository struct {
	sessions databaseAbstract.DbRepository[gameSessionDocument]
	outbox   databaseAbstract.DbRepository[entities.OutboxMessage]
	receipts databaseAbstract.DbRepository[entities.GameSessionCommandReceipt]
}

var _ gameAbstract.GameSessionRepository = (*GameSessionRepository)(nil)

func NewGameSessionRepository(client *mongo.Client, dbName string) *GameSessionRepository {
	return &GameSessionRepository{
		sessions: NewDbContext[gameSessionDocument](client, dbName),
		outbox:   NewDbContext[entities.OutboxMessage](client, dbName),
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
	messages, err := outboxMessages(events)
	if err != nil {
		return gameAbstract.PersistenceResult{}, err
	}
	receipt := commandReceipt(command, snapshot.SessionID)

	err = repository.sessions.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		if _, err := repository.receipts.Insert(txCtx, receipt); err != nil {
			return err
		}
		if _, err := repository.sessions.Insert(txCtx, document); err != nil {
			return err
		}
		return repository.outbox.InsertMany(txCtx, messages)
	})
	if mongo.IsDuplicateKeyError(err) {
		result, found, lookupErr := repository.FindProcessedCommand(ctx, command)
		if lookupErr != nil {
			return gameAbstract.PersistenceResult{}, lookupErr
		}
		if found {
			return result, nil
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

func (repository *GameSessionRepository) FindProcessedCommand(
	ctx context.Context,
	command gameAbstract.CommandDescriptor,
) (gameAbstract.PersistenceResult, bool, error) {
	if err := validateCommandDescriptor(command); err != nil {
		return gameAbstract.PersistenceResult{}, false, err
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
	document := gameSessionDocumentFromSnapshot(snapshot)
	messages, err := outboxMessages(events)
	if err != nil {
		return gameAbstract.PersistenceResult{}, err
	}
	receipt := commandReceipt(command, snapshot.SessionID)

	err = repository.sessions.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		if _, err := repository.receipts.Insert(txCtx, receipt); err != nil {
			return err
		}
		result, err := repository.sessions.Collection().ReplaceOne(txCtx, bson.M{
			"_id":     snapshot.SessionID,
			"version": expectedVersion,
		}, document)
		if err != nil {
			return err
		}
		if result.MatchedCount == 0 {
			exists, err := repository.sessions.ExistsByFilter(txCtx, bson.M{"_id": snapshot.SessionID})
			if err != nil {
				return err
			}
			if !exists {
				return gameAbstract.ErrGameSessionNotFound
			}
			return gameAbstract.ErrConcurrentSessionUpdate
		}
		return repository.outbox.InsertMany(txCtx, messages)
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

func outboxMessages(events []gameDomain.SessionEvent) ([]entities.OutboxMessage, error) {
	createdAt := time.Now().UTC()
	messages := make([]entities.OutboxMessage, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		messages = append(messages, entities.OutboxMessage{
			EventID:          event.EventID,
			AggregateType:    gameSessionAggregateType,
			AggregateID:      event.SessionID,
			AggregateVersion: event.Version,
			EventType:        string(event.Type),
			Payload:          payload,
			OccurredAt:       event.OccurredAt,
			CreatedAt:        createdAt,
			NextAttemptAt:    createdAt,
		})
	}
	return messages, nil
}
