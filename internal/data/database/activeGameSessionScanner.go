package database

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	gameAbstract "houseflowApi/internal/application/game/abstract"
)

// ListActiveSessions pages the unique partial activeKey index, so history is
// never scanned; the activeKey itself is the page cursor.
func (repository *GameSessionRepository) ListActiveSessions(ctx context.Context, after string, limit int) ([]gameAbstract.ActiveGameSession, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("invalid active session page size")
	}
	cursor, err := repository.sessions.Collection().Find(ctx, bson.M{"activeKey": bson.M{"$exists": true, "$gt": after}}, options.Find().SetHint("uqGameSessionActiveKey").SetSort(bson.D{{Key: "activeKey", Value: 1}}).SetLimit(int64(limit)).SetProjection(bson.M{"gameKey": 1, "activeKey": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]gameAbstract.ActiveGameSession, 0, limit)
	for cursor.Next(ctx) {
		var document struct {
			SessionID string `bson:"_id"`
			GameKey   string `bson:"gameKey"`
			ActiveKey string `bson:"activeKey"`
		}
		if err := cursor.Decode(&document); err != nil {
			return nil, err
		}
		result = append(result, gameAbstract.ActiveGameSession{SessionID: document.SessionID, GameKey: document.GameKey, Cursor: document.ActiveKey})
	}
	return result, cursor.Err()
}

var _ gameAbstract.ActiveGameSessionScanner = (*GameSessionRepository)(nil)
