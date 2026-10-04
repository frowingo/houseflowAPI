package database

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	gameAbstract "houseflowApi/internal/application/game/abstract"
)

func (repository *GameSessionRepository) ListActiveSessions(ctx context.Context, afterSessionID string, limit int) ([]gameAbstract.ActiveGameSession, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("invalid active session page size")
	}
	cursor, err := repository.activeSessions.Collection().Find(ctx, bson.M{"sessionId": bson.M{"$gt": afterSessionID}}, options.Find().SetHint("uqActiveGameSessionSession").SetSort(bson.D{{Key: "sessionId", Value: 1}}).SetLimit(int64(limit)).SetProjection(bson.M{"sessionId": 1, "gameKey": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]gameAbstract.ActiveGameSession, 0, limit)
	for cursor.Next(ctx) {
		var document struct {
			SessionID string `bson:"sessionId"`
			GameKey   string `bson:"gameKey"`
		}
		if err := cursor.Decode(&document); err != nil {
			return nil, err
		}
		result = append(result, gameAbstract.ActiveGameSession{SessionID: document.SessionID, GameKey: document.GameKey})
	}
	return result, cursor.Err()
}

var _ gameAbstract.ActiveGameSessionScanner = (*GameSessionRepository)(nil)
