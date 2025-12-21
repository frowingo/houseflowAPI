package database

import (
	"context"
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
		panic(err)
	}

	clientOpts := options.Client().ApplyURI(config.External.Mongo.DevConString)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		panic(err)
	}

	collection := client.Database(config.External.Mongo.DbName).Collection(colName)

	return &MongoDbContext{
		Client:     client,
		Collection: collection,
	}, nil

}

func (r *MongoDbContext) CloseConnection(ctx context.Context) error {
	return r.Client.Disconnect(ctx)
}
