package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const GameSessionCommandReceiptCollectionName = "GameSessionCommandReceipt"

type GameSessionCommandReceipt struct {
	Id          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ActorID     string             `bson:"actorId" json:"actorId"`
	CommandID   string             `bson:"commandId" json:"commandId"`
	CommandType string             `bson:"commandType" json:"commandType"`
	PayloadHash string             `bson:"payloadHash" json:"payloadHash"`
	SessionID   string             `bson:"sessionId" json:"sessionId"`
	ProcessedAt time.Time          `bson:"processedAt" json:"processedAt"`
}

func (GameSessionCommandReceipt) CollectionName() string {
	return GameSessionCommandReceiptCollectionName
}
