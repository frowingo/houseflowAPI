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
}

var _ gameAbstract.GameSessionRepository = (*GameSessionRepository)(nil)

func NewGameSessionRepository(client *mongo.Client, dbName string) *GameSessionRepository {
	return &GameSessionRepository{
		sessions: NewDbContext[gameSessionDocument](client, dbName),
		outbox:   NewDbContext[entities.OutboxMessage](client, dbName),
	}
}

func (repository *GameSessionRepository) Create(
	ctx context.Context,
	snapshot gameDomain.SessionSnapshot,
	events []gameDomain.SessionEvent,
) error {
	if err := validatePersistenceBatch(0, snapshot, events); err != nil {
		return err
	}
	document := gameSessionDocumentFromSnapshot(snapshot)
	messages, err := outboxMessages(events)
	if err != nil {
		return err
	}

	err = repository.sessions.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		if _, err := repository.sessions.Insert(txCtx, document); err != nil {
			return err
		}
		return repository.outbox.InsertMany(txCtx, messages)
	})
	if mongo.IsDuplicateKeyError(err) {
		return gameAbstract.ErrGameSessionAlreadyExists
	}
	return err
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

func (repository *GameSessionRepository) Save(
	ctx context.Context,
	expectedVersion int64,
	snapshot gameDomain.SessionSnapshot,
	events []gameDomain.SessionEvent,
) error {
	if err := validatePersistenceBatch(expectedVersion, snapshot, events); err != nil {
		return err
	}
	document := gameSessionDocumentFromSnapshot(snapshot)
	messages, err := outboxMessages(events)
	if err != nil {
		return err
	}

	return repository.sessions.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
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
}

func validatePersistenceBatch(
	expectedVersion int64,
	snapshot gameDomain.SessionSnapshot,
	events []gameDomain.SessionEvent,
) error {
	if expectedVersion < 0 || snapshot.Version <= expectedVersion || len(events) == 0 {
		return gameAbstract.ErrInvalidPersistenceBatch
	}
	if _, err := gameDomain.RestoreGameSession(snapshot); err != nil {
		return errors.Join(gameAbstract.ErrInvalidPersistenceBatch, err)
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
