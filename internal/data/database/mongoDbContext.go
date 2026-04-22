package database

import (
	"context"
	"errors"
	"houseflowApi/internal/config"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoDbContext struct {
	Client     *mongo.Client
	Collection *mongo.Collection
}

func (r *MongoDbContext) NewConnection(ctx context.Context, colName string) (*MongoDbContext, error) {

	config, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}

	clientOpts := options.Client().ApplyURI(config.External.Mongo.ConnectionString)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, errors.New("don't create amongo client:" + err.Error())
	}

	collection := client.Database(config.External.Mongo.DbName).Collection(colName)

	return &MongoDbContext{
		Client:     client,
		Collection: collection,
	}, nil

}

func (r *MongoDbContext) CloseConnection(ctx context.Context) error {
	if r.Client == nil {
		return errors.New("mongo context can't close, is null")
	}
	return r.Client.Disconnect(ctx)
}

// NewDatabase returns a *mongo.Database using the configured connection string and db name.
// Caller is responsible for disconnecting the returned client.
func NewDatabase(ctx context.Context) (*mongo.Client, *mongo.Database, error) {
	cfg, err := config.LoadConfig()
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
