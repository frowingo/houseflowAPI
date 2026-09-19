package entities

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const OutboxMessageCollectionName = "OutboxMessage"

type OutboxMessage struct {
	Id               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	EventID          string             `bson:"eventId" json:"eventId"`
	AggregateType    string             `bson:"aggregateType" json:"aggregateType"`
	AggregateID      string             `bson:"aggregateId" json:"aggregateId"`
	AggregateVersion int64              `bson:"aggregateVersion" json:"aggregateVersion"`
	EventType        string             `bson:"eventType" json:"eventType"`
	Payload          []byte             `bson:"payload" json:"payload"`
	OccurredAt       time.Time          `bson:"occurredAt" json:"occurredAt"`
	CreatedAt        time.Time          `bson:"createdAt" json:"createdAt"`
	PublishedAt      *time.Time         `bson:"publishedAt,omitempty" json:"publishedAt,omitempty"`
	AttemptCount     int                `bson:"attemptCount" json:"attemptCount"`
	NextAttemptAt    time.Time          `bson:"nextAttemptAt" json:"nextAttemptAt"`
	ClaimedBy        string             `bson:"claimedBy,omitempty" json:"claimedBy,omitempty"`
	ClaimedUntil     *time.Time         `bson:"claimedUntil,omitempty" json:"claimedUntil,omitempty"`
	LastError        string             `bson:"lastError,omitempty" json:"lastError,omitempty"`
}

func (OutboxMessage) CollectionName() string {
	return OutboxMessageCollectionName
}
