package abstract

import (
	"context"
	"houseflowApi/internal/helpers"
	"reflect"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type DbRepository[T any] struct {
	client *mongo.Client
	dbName string
}

type collectionNameProvider interface {
	CollectionName() string
}

func New[T any](client *mongo.Client, dbName string) *DbRepository[T] {
	return &DbRepository[T]{client: client, dbName: dbName}
}

func (r *DbRepository[T]) getCollection() *mongo.Collection {
	var zero T
	if provider, ok := any(zero).(collectionNameProvider); ok {
		return r.client.Database(r.dbName).Collection(provider.CollectionName())
	}
	if provider, ok := any(&zero).(collectionNameProvider); ok {
		return r.client.Database(r.dbName).Collection(provider.CollectionName())
	}

	entityType := reflect.TypeOf(new(T)).Elem()
	return r.client.Database(r.dbName).Collection(entityType.Name())
}

// Collection exposes the repository's MongoDB collection for use-case-specific
// atomic and conditional operations that are not part of the generic API.
func (r *DbRepository[T]) Collection() *mongo.Collection {
	return r.getCollection()
}

// WithinTransaction executes fn with MongoDB's retryable transaction callback.
// The callback must not perform non-database side effects because it may run
// more than once.
func (r *DbRepository[T]) WithinTransaction(ctx context.Context, fn func(mongo.SessionContext) error) error {
	session, err := r.client.StartSession()
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		session.EndSession(cleanupCtx)
	}()

	_, err = session.WithTransaction(ctx, func(txCtx mongo.SessionContext) (interface{}, error) {
		return nil, fn(txCtx)
	}, options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority()).
		SetReadPreference(readpref.Primary()))
	return err
}

func (r *DbRepository[T]) Insert(entity T) (*T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.InsertContext(ctx, entity)
}

func (r *DbRepository[T]) InsertContext(ctx context.Context, entity T) (*T, error) {
	entityVal := reflect.ValueOf(&entity).Elem()
	idField := entityVal.FieldByName("Id")
	if idField.IsValid() && idField.CanSet() && idField.Interface() == (primitive.ObjectID{}) {
		idField.Set(reflect.ValueOf(primitive.NewObjectID()))
	}
	if _, err := r.getCollection().InsertOne(ctx, entity); err != nil {
		return nil, err
	}
	return &entity, nil
}

func (r *DbRepository[T]) InsertManyContext(ctx context.Context, entities []T) error {
	if len(entities) == 0 {
		return nil
	}
	documents := make([]any, 0, len(entities))
	for _, entity := range entities {
		documents = append(documents, entity)
	}
	_, err := r.getCollection().InsertMany(ctx, documents)
	return err
}

func (r *DbRepository[T]) FindByID(id primitive.ObjectID) (*T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.FindByIDContext(ctx, id)
}

func (r *DbRepository[T]) FindByIDContext(ctx context.Context, id primitive.ObjectID) (*T, error) {
	var result T
	if err := r.getCollection().FindOne(ctx, bson.M{"_id": id}).Decode(&result); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, helpers.NewLocalizedError("database.error.document_not_found")
		}
		return nil, err
	}
	return &result, nil
}

// this method only for string columns
func (r *DbRepository[T]) FindByColumn(columnName string, columnValue string) (*T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.FindByColumnContext(ctx, columnName, columnValue)
}

func (r *DbRepository[T]) FindByColumnContext(ctx context.Context, columnName string, columnValue string) (*T, error) {
	var result T
	if err := r.getCollection().FindOne(ctx, bson.M{columnName: columnValue}).Decode(&result); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, helpers.NewLocalizedError("database.error.document_not_found")
		}
		return nil, err
	}
	return &result, nil
}

// TODO : add pagination absolutly !!!
// learn -> how to use cursor by mongo
func (r *DbRepository[T]) FindAll() ([]T, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	collection := r.getCollection()

	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		var zero []T
		return zero, err
	}
	defer cursor.Close(ctx)

	var results []T
	if err = cursor.All(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (r *DbRepository[T]) FindManyByColumn(columnName string, columnValue string) ([]T, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	collection := r.getCollection()

	cursor, err := collection.Find(ctx, bson.M{columnName: columnValue})
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

func (r *DbRepository[T]) UpdateFields(id primitive.ObjectID, fields bson.M) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.UpdateFieldsContext(ctx, id, fields)
}

func (r *DbRepository[T]) UpdateFieldsContext(ctx context.Context, id primitive.ObjectID, fields bson.M) error {
	result, err := r.getCollection().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": fields})
	if err != nil {
		return err
	}

	if result.MatchedCount == 0 {
		return helpers.NewLocalizedError("database.error.document_not_found")
	}

	return nil
}

func (r *DbRepository[T]) FindManyByFilter(filter bson.M) ([]T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.FindManyByFilterContext(ctx, filter)
}

func (r *DbRepository[T]) FindManyByFilterContext(ctx context.Context, filter bson.M, opts ...*options.FindOptions) ([]T, error) {
	cursor, err := r.getCollection().Find(ctx, filter, opts...)
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

func (r *DbRepository[T]) ExistsByFilter(filter bson.M) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.ExistsByFilterContext(ctx, filter)
}

func (r *DbRepository[T]) ExistsByFilterContext(ctx context.Context, filter bson.M) (bool, error) {
	count, err := r.getCollection().CountDocuments(ctx, filter, options.Count().SetLimit(1))
	return count > 0, err
}

func (r *DbRepository[T]) Delete(id primitive.ObjectID) error {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	collection := r.getCollection()

	result, err := collection.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}

	if result.DeletedCount == 0 {
		return helpers.NewLocalizedError("database.error.delete_not_found")
	}

	return nil
}
