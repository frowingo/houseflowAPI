package database

import (
	"context"
	"errors"
	"houseflowApi/internal/config"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

const (
	mongoConnectTimeout         = 5 * time.Second
	mongoServerSelectionTimeout = 5 * time.Second
)

// NewDatabase returns a *mongo.Database using the configured connection string and db name.
// Caller is responsible for disconnecting the returned client.
func NewDatabase(ctx context.Context, cfg config.ConfigMongo) (*mongo.Client, *mongo.Database, error) {
	clientOpts := options.Client().
		ApplyURI(cfg.ConnectionString).
		SetConnectTimeout(mongoConnectTimeout).
		SetServerSelectionTimeout(mongoServerSelectionTimeout)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, nil, errors.New("failed to connect to mongo: " + err.Error())
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), mongoConnectTimeout)
		defer cancel()
		_ = client.Disconnect(cleanupCtx)
		return nil, nil, errors.New("failed to ping mongo: " + err.Error())
	}

	db := client.Database(cfg.DbName)
	return client, db, nil
}
