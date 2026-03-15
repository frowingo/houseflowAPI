package abstract

import (
	"context"
	"errors"
	"houseflowApi/internal/data/database"
	"reflect"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type DbRepository[T any] struct {
	mongoContext *database.MongoDbContext
}

func New[T any]() *DbRepository[T] {
	return &DbRepository[T]{}
}

func (r *DbRepository[T]) Insert(entity T) (*T, error) {

	ctx := context.Background()

	entityType := reflect.TypeOf(entity)
	if entityType.Kind() == reflect.Ptr {
		entityType = entityType.Elem()
	}

	colName := entityType.Name()

	mongoCtx, err := r.mongoContext.NewConnection(ctx, colName)
	if err != nil {
		var zero *T
		return zero, err
	}
	defer mongoCtx.CloseConnection(ctx)

	_, err = mongoCtx.Collection.InsertOne(ctx, entity)
	if err != nil {
		var zero *T
		return zero, err
	}

	return &entity, nil
}

func (r *DbRepository[T]) FindById(id primitive.ObjectID) (*T, error) {

	ctx := context.Background()

	entityType := reflect.TypeOf(new(T)).Elem()
	colName := entityType.Name()

	mongoCtx, err := r.mongoContext.NewConnection(ctx, colName)
	if err != nil {
		var zero *T
		return zero, err
	}
	defer mongoCtx.CloseConnection(ctx)

	var result T
	err = mongoCtx.Collection.FindOne(ctx, bson.M{"_id": id}).Decode(&result)
	if err != nil {
		return nil, errors.New("document not found")
	}

	return &result, nil
}

// this method only for string columns
func (r *DbRepository[T]) FindByColumn(columnName string, columnValue string) (*T, error) {

	ctx := context.Background()

	entityType := reflect.TypeOf(new(T)).Elem()
	colName := entityType.Name()

	mongoCtx, err := r.mongoContext.NewConnection(ctx, colName)
	if err != nil {
		var zero *T
		return zero, err
	}
	defer mongoCtx.CloseConnection(ctx)

	var result T
	err = mongoCtx.Collection.FindOne(ctx, bson.M{columnName: columnValue}).Decode(&result)
	if err == mongo.ErrNoDocuments {
		return nil, errors.New("document not found")
	} else if err != nil {
		return nil, errors.New("kime bakmıştınız:" + err.Error())
	}

	return &result, nil
}

// TODO : add pagination absolutly !!!
// learn -> how to use cursor by mongo
func (r *DbRepository[T]) FindAll() ([]T, error) {

	ctx := context.Background()

	entityType := reflect.TypeOf(new(T)).Elem()
	colName := entityType.Name()

	mongoCtx, err := r.mongoContext.NewConnection(ctx, colName)
	if err != nil {
		var zero []T
		return zero, err
	}
	defer mongoCtx.CloseConnection(ctx)

	cursor, err := mongoCtx.Collection.Find(ctx, bson.M{})
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

	ctx := context.Background()

	entityType := reflect.TypeOf(new(T)).Elem()
	colName := entityType.Name()

	mongoCtx, err := r.mongoContext.NewConnection(ctx, colName)
	if err != nil {
		var zero *T
		return zero, err
	}
	defer mongoCtx.CloseConnection(ctx)

	result, err := mongoCtx.Collection.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": updatedEntity})
	if err != nil {
		var zero *T
		return zero, err
	}

	if result.MatchedCount == 0 {
		return nil, errors.New("Olmayan şeyi nasıl güncelliyim altan mal mısın?")
	}

	return &updatedEntity, nil
}

func (r *DbRepository[T]) FindManyByColumn(columnName string, columnValue string) ([]T, error) {

	ctx := context.Background()

	entityType := reflect.TypeOf(new(T)).Elem()
	colName := entityType.Name()

	mongoCtx, err := r.mongoContext.NewConnection(ctx, colName)
	if err != nil {
		return nil, err
	}
	defer mongoCtx.CloseConnection(ctx)

	cursor, err := mongoCtx.Collection.Find(ctx, bson.M{columnName: columnValue})
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

	ctx := context.Background()

	entityType := reflect.TypeOf(new(T)).Elem()
	colName := entityType.Name()

	mongoCtx, err := r.mongoContext.NewConnection(ctx, colName)
	if err != nil {
		return err
	}
	defer mongoCtx.CloseConnection(ctx)

	result, err := mongoCtx.Collection.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": fields})
	if err != nil {
		return err
	}

	if result.MatchedCount == 0 {
		return errors.New("user not found")
	}

	return nil
}

func (r *DbRepository[T]) Delete(id primitive.ObjectID) error {

	ctx := context.Background()

	entityType := reflect.TypeOf(new(T)).Elem()
	colName := entityType.Name()

	mongoCtx, err := r.mongoContext.NewConnection(ctx, colName)
	if err != nil {
		return err
	}
	defer mongoCtx.CloseConnection(ctx)

	result, err := mongoCtx.Collection.DeleteOne(ctx, bson.M{"_id": id})

	if result.DeletedCount == 0 {
		return errors.New("Olmayan şeyi nasıl silerim altan mal mısın?")
	}

	return nil
}
