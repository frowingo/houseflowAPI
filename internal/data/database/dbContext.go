package database

import (
	"context"
	"errors"
	"reflect"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/helpers"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type DbContext[T any] struct {
	client *mongo.Client
	dbName string
}

type collectionNameProvider interface {
	CollectionName() string
}

func NewDbContext[T any](client *mongo.Client, dbName string) *DbContext[T] {
	return &DbContext[T]{client: client, dbName: dbName}
}

func (c *DbContext[T]) getCollection() *mongo.Collection {
	var entity T
	if provider, ok := any(entity).(collectionNameProvider); ok {
		return c.client.Database(c.dbName).Collection(provider.CollectionName())
	}
	if provider, ok := any(&entity).(collectionNameProvider); ok {
		return c.client.Database(c.dbName).Collection(provider.CollectionName())
	}

	entityType := reflect.TypeOf(new(T)).Elem()
	return c.client.Database(c.dbName).Collection(entityType.Name())
}

func (c *DbContext[T]) Collection() *mongo.Collection {
	return c.getCollection()
}

func (c *DbContext[T]) WithinTransaction(ctx context.Context, operation func(mongo.SessionContext) error) error {
	session, err := c.client.StartSession()
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		session.EndSession(cleanupCtx)
	}()

	_, err = session.WithTransaction(ctx, func(transactionCtx mongo.SessionContext) (interface{}, error) {
		return nil, operation(transactionCtx)
	}, options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority()).
		SetReadPreference(readpref.Primary()))
	if err == nil {
		return nil
	}
	var serverError mongo.ServerError
	if mongo.IsNetworkError(err) || mongo.IsTimeout(err) ||
		(errors.As(err, &serverError) && (serverError.HasErrorLabel("TransientTransactionError") ||
			serverError.HasErrorLabel("UnknownTransactionCommitResult"))) {
		return helpers.NewUnavailableError("database.error.transaction_unavailable", err)
	}
	return err
}

func (c *DbContext[T]) Insert(ctx context.Context, entity T) (*T, error) {
	entityValue := reflect.ValueOf(&entity).Elem()
	idField := entityValue.FieldByName("Id")
	if idField.IsValid() && idField.CanSet() && idField.Interface() == (primitive.ObjectID{}) {
		idField.Set(reflect.ValueOf(primitive.NewObjectID()))
	}
	if _, err := c.getCollection().InsertOne(ctx, entity); err != nil {
		return nil, err
	}
	return &entity, nil
}

func (c *DbContext[T]) InsertMany(ctx context.Context, entities []T) error {
	if len(entities) == 0 {
		return nil
	}
	documents := make([]any, 0, len(entities))
	for _, entity := range entities {
		documents = append(documents, entity)
	}
	_, err := c.getCollection().InsertMany(ctx, documents)
	return err
}

func (c *DbContext[T]) FindByID(ctx context.Context, id primitive.ObjectID) (*T, error) {
	var result T
	if err := c.getCollection().FindOne(ctx, bson.M{"_id": id}).Decode(&result); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, helpers.NewNotFoundError("database.error.document_not_found")
		}
		return nil, err
	}
	return &result, nil
}

func (c *DbContext[T]) FindByColumn(ctx context.Context, columnName string, columnValue string) (*T, error) {
	var result T
	if err := c.getCollection().FindOne(ctx, bson.M{columnName: columnValue}).Decode(&result); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, helpers.NewNotFoundError("database.error.document_not_found")
		}
		return nil, err
	}
	return &result, nil
}

func (c *DbContext[T]) FindAll(ctx context.Context) ([]T, error) {
	cursor, err := c.getCollection().Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []T
	if err = cursor.All(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (c *DbContext[T]) FindManyByColumn(ctx context.Context, columnName string, columnValue string) ([]T, error) {
	cursor, err := c.getCollection().Find(ctx, bson.M{columnName: columnValue})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []T
	if err = cursor.All(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (c *DbContext[T]) UpdateFields(ctx context.Context, id primitive.ObjectID, fields bson.M) error {
	result, err := c.getCollection().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": fields})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return helpers.NewNotFoundError("database.error.document_not_found")
	}
	return nil
}

func (c *DbContext[T]) FindManyByFilter(ctx context.Context, filter bson.M, findOptions ...*options.FindOptions) ([]T, error) {
	cursor, err := c.getCollection().Find(ctx, filter, findOptions...)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []T
	if err = cursor.All(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (c *DbContext[T]) ExistsByFilter(ctx context.Context, filter bson.M) (bool, error) {
	count, err := c.getCollection().CountDocuments(ctx, filter, options.Count().SetLimit(1))
	return count > 0, err
}

func (c *DbContext[T]) Delete(ctx context.Context, id primitive.ObjectID) error {
	result, err := c.getCollection().DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return helpers.NewNotFoundError("database.error.delete_not_found")
	}
	return nil
}

var _ databaseAbstract.DbRepository[any] = (*DbContext[any])(nil)
