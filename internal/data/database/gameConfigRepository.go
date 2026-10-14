package database

import (
	"context"
	"errors"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const GameConfigCollectionName = "Game"

type gameRolloutDocument struct {
	HouseIDs []string `bson:"houseIds"`
}

type gameConfigDocument struct {
	GameKey   string              `bson:"_id"`
	Enabled   bool                `bson:"enabled"`
	Rollout   gameRolloutDocument `bson:"rollout"`
	Rules     bson.M              `bson:"rules,omitempty"`
	Settings  bson.M              `bson:"settings,omitempty"`
	Version   int64               `bson:"version"`
	UpdatedAt time.Time           `bson:"updatedAt"`
	UpdatedBy string              `bson:"updatedBy,omitempty"`
}

func (document gameConfigDocument) record() gameAbstract.GameConfigRecord {
	return gameAbstract.GameConfigRecord{GameKey: document.GameKey, Version: document.Version, Enabled: document.Enabled, HouseIDs: document.Rollout.HouseIDs,
		Rules: documentJSON(document.Rules), Settings: documentJSON(document.Settings), UpdatedAt: document.UpdatedAt, UpdatedBy: document.UpdatedBy}
}

type GameConfigRepository struct {
	games *mongo.Collection
}

func NewGameConfigRepository(client *mongo.Client, dbName string) *GameConfigRepository {
	return &GameConfigRepository{client.Database(dbName).Collection(GameConfigCollectionName)}
}

func (repository *GameConfigRepository) List(ctx context.Context) ([]gameAbstract.GameConfigRecord, error) {
	cursor, err := repository.games.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	var documents []gameConfigDocument
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, err
	}
	records := make([]gameAbstract.GameConfigRecord, 0, len(documents))
	for _, document := range documents {
		records = append(records, document.record())
	}
	return records, nil
}

func (repository *GameConfigRepository) Save(ctx context.Context, record gameAbstract.GameConfigRecord, expectedVersion int64) (gameAbstract.GameConfigRecord, error) {
	if record.GameKey == "" || expectedVersion < 0 {
		return gameAbstract.GameConfigRecord{}, errors.New("game key and a non-negative version are required")
	}
	rules, err := jsonDocument(record.Rules)
	if err != nil {
		return gameAbstract.GameConfigRecord{}, err
	}
	settings, err := jsonDocument(record.Settings)
	if err != nil {
		return gameAbstract.GameConfigRecord{}, err
	}
	houseIDs := record.HouseIDs
	if houseIDs == nil {
		houseIDs = []string{}
	}
	document := gameConfigDocument{GameKey: record.GameKey, Enabled: record.Enabled, Rollout: gameRolloutDocument{houseIDs}, Rules: rules, Settings: settings,
		Version: expectedVersion + 1, UpdatedAt: time.Now().UTC().Truncate(time.Millisecond), UpdatedBy: record.UpdatedBy}
	if expectedVersion == 0 {
		_, err = repository.games.InsertOne(ctx, document)
	} else {
		err = repository.games.FindOneAndReplace(ctx, bson.M{"_id": record.GameKey, "version": expectedVersion}, document, options.FindOneAndReplace().SetProjection(bson.M{"_id": 1})).Err()
	}
	if mongo.IsDuplicateKeyError(err) || errors.Is(err, mongo.ErrNoDocuments) {
		return gameAbstract.GameConfigRecord{}, gameAbstract.ErrGameConfigConflict
	}
	if err != nil {
		return gameAbstract.GameConfigRecord{}, err
	}
	return document.record(), nil
}

var _ gameAbstract.GameConfigRepository = (*GameConfigRepository)(nil)
