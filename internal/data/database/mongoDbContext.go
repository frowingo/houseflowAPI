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

	clientOpts := options.Client().ApplyURI(config.External.Mongo.DevConString)
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
