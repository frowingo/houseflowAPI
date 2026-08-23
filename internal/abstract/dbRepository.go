package abstract

import (
	"context"
	"houseflowApi/internal/helpers"
	"reflect"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
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

func (r *DbRepository[T]) Insert(entity T) (*T, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	collection := r.getCollection()

	// Assign a new ObjectID before insert so the returned entity already has it.
	entityVal := reflect.ValueOf(&entity).Elem()
	idField := entityVal.FieldByName("Id")
	if idField.IsValid() && idField.CanSet() && idField.Interface() == (primitive.ObjectID{}) {
		idField.Set(reflect.ValueOf(primitive.NewObjectID()))
	}

	_, err := collection.InsertOne(ctx, entity)
	if err != nil {
		var zero *T
		return zero, err
	}

	return &entity, nil
}

func (r *DbRepository[T]) InsertMany(entities []T) error {
	if len(entities) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	documents := make([]any, 0, len(entities))
	for _, entity := range entities {
		documents = append(documents, entity)
	}

	_, err := r.getCollection().InsertMany(ctx, documents)
	return err
}

func (r *DbRepository[T]) FindById(id primitive.ObjectID) (*T, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	collection := r.getCollection()

	var result T
	err := collection.FindOne(ctx, bson.M{"_id": id}).Decode(&result)
	if err != nil {
		return nil, helpers.NewLocalizedError("database.error.document_not_found")
	}

	return &result, nil
}

// this method only for string columns
func (r *DbRepository[T]) FindByColumn(columnName string, columnValue string) (*T, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	collection := r.getCollection()

	var result T
	err := collection.FindOne(ctx, bson.M{columnName: columnValue}).Decode(&result)
	if err == mongo.ErrNoDocuments {
		return nil, helpers.NewLocalizedError("database.error.document_not_found")
	} else if err != nil {
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

func (r *DbRepository[T]) Update(id primitive.ObjectID, updatedEntity T) (*T, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	collection := r.getCollection()

	updateFields := bson.M{}
	payload, err := bson.Marshal(updatedEntity)
	if err != nil {
		var zero *T
		return zero, err
	}
	if err := bson.Unmarshal(payload, &updateFields); err != nil {
		var zero *T
		return zero, err
	}
	delete(updateFields, "_id")

	result, err := collection.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": updateFields})
	if err != nil {
		var zero *T
		return zero, err
	}

	if result.MatchedCount == 0 {
		return nil, helpers.NewLocalizedError("database.error.update_not_found")
	}

	return &updatedEntity, nil
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

	collection := r.getCollection()

	result, err := collection.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": fields})
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

	collection := r.getCollection()

	cursor, err := collection.Find(ctx, filter)
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

	collection := r.getCollection()

	count, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		return false, err
	}

	return count > 0, nil
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
