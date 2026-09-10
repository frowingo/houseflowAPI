package abstract

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DbRepository[T any] interface {
	Collection() *mongo.Collection
	WithinTransaction(ctx context.Context, operation func(mongo.SessionContext) error) error
	Insert(ctx context.Context, entity T) (*T, error)
	InsertMany(ctx context.Context, entities []T) error
	FindByID(ctx context.Context, id primitive.ObjectID) (*T, error)
	FindByColumn(ctx context.Context, columnName string, columnValue string) (*T, error)
	FindAll(ctx context.Context) ([]T, error)
	FindManyByColumn(ctx context.Context, columnName string, columnValue string) ([]T, error)
	UpdateFields(ctx context.Context, id primitive.ObjectID, fields bson.M) error
	FindManyByFilter(ctx context.Context, filter bson.M, findOptions ...*options.FindOptions) ([]T, error)
	ExistsByFilter(ctx context.Context, filter bson.M) (bool, error)
	Delete(ctx context.Context, id primitive.ObjectID) error
}
