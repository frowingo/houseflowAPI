package database

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const ActiveGameSessionCollectionName = "ActiveGameSession"

type activeGameSessionDocument struct {
	Id        primitive.ObjectID `bson:"_id,omitempty"`
	HouseID   string             `bson:"houseId"`
	GameKey   string             `bson:"gameKey"`
	SessionID string             `bson:"sessionId"`
	CreatedAt time.Time          `bson:"createdAt"`
}

func (activeGameSessionDocument) CollectionName() string {
	return ActiveGameSessionCollectionName
}
