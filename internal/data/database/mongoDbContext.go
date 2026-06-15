package database

import (
	"context"
	"errors"
	"houseflowApi/internal/config"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// NewDatabase returns a *mongo.Database using the configured connection string and db name.
// Caller is responsible for disconnecting the returned client.
func NewDatabase(ctx context.Context) (*mongo.Client, *mongo.Database, error) {
	cfg, err := config.MustLoadConfig()
	if err != nil {
		return nil, nil, err
	}

	clientOpts := options.Client().ApplyURI(cfg.External.Mongo.ConnectionString)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, nil, errors.New("failed to connect to mongo: " + err.Error())
	}

	db := client.Database(cfg.External.Mongo.DbName)
	return client, db, nil
}
