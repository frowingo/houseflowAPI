package database

import (
	"context"
	"errors"
	"math"

	gameAbstract "houseflowApi/internal/application/game/abstract"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const RuntimeOwnerCollectionName = "GameRuntimeOwner"

type runtimeOwnerDocument struct {
	SessionID       string `bson:"_id"`
	Generation      int64  `bson:"generation"`
	OwnerInstanceID string `bson:"ownerInstanceId"`
	LeaseID         string `bson:"leaseId"`
	Started         bool   `bson:"started"`
}

func (document runtimeOwnerDocument) owner() gameAbstract.RuntimeOwner {
	return gameAbstract.RuntimeOwner{SessionID: document.SessionID, Generation: document.Generation, OwnerInstanceID: document.OwnerInstanceID, LeaseID: document.LeaseID, Started: document.Started}
}

var _ gameAbstract.RuntimeOwnerRepository = (*GameSessionRepository)(nil)

func (repository *GameSessionRepository) FindRuntimeOwner(ctx context.Context, sessionID string) (gameAbstract.RuntimeOwner, error) {
	var document runtimeOwnerDocument
	if sessionID == "" {
		return gameAbstract.RuntimeOwner{}, gameAbstract.ErrRuntimeOwnerConflict
	}
	err := repository.sessions.Collection().Database().Collection(RuntimeOwnerCollectionName).FindOne(ctx, bson.M{"_id": sessionID}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return gameAbstract.RuntimeOwner{SessionID: sessionID}, nil
	}
	return document.owner(), err
}

// Read expectedGeneration BEFORE acquiring the Redis lease. Never reload/retry a
// failed CAS under the same lease: a delayed old claimant must not fence a new owner.
func (repository *GameSessionRepository) ClaimRuntimeOwner(ctx context.Context, expectedGeneration int64, owner gameAbstract.RuntimeOwner) (gameAbstract.RuntimeOwner, error) {
	if expectedGeneration < 0 || expectedGeneration == math.MaxInt64 || owner.SessionID == "" || owner.OwnerInstanceID == "" || owner.LeaseID == "" {
		return gameAbstract.RuntimeOwner{}, gameAbstract.ErrRuntimeOwnerConflict
	}
	collection := repository.sessions.Collection().Database().Collection(RuntimeOwnerCollectionName)
	var claimed runtimeOwnerDocument
	err := collection.FindOneAndUpdate(ctx, bson.M{"_id": owner.SessionID, "generation": expectedGeneration}, bson.M{
		"$inc":         bson.M{"generation": int64(1)},
		"$set":         bson.M{"ownerInstanceId": owner.OwnerInstanceID, "leaseId": owner.LeaseID},
		"$setOnInsert": bson.M{"started": false},
	}, options.FindOneAndUpdate().SetUpsert(expectedGeneration == 0).SetReturnDocument(options.After)).Decode(&claimed)
	if errors.Is(err, mongo.ErrNoDocuments) || mongo.IsDuplicateKeyError(err) {
		return gameAbstract.RuntimeOwner{}, gameAbstract.ErrRuntimeOwnerConflict
	}
	return claimed.owner(), err
}

// A runtime may start from spawn only once, even after lease loss or Redis reset.
// Checkpoint recovery deliberately remains closed until the recovery package.
func (repository *GameSessionRepository) MarkRuntimeStarted(ctx context.Context, owner gameAbstract.RuntimeOwner) error {
	result, err := repository.sessions.Collection().Database().Collection(RuntimeOwnerCollectionName).UpdateOne(ctx, bson.M{
		"_id": owner.SessionID, "generation": owner.Generation, "ownerInstanceId": owner.OwnerInstanceID, "leaseId": owner.LeaseID, "started": false,
	}, bson.M{"$set": bson.M{"started": true}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return gameAbstract.ErrRuntimeRecoveryRequired
	}
	return nil
}
