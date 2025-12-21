package abstract

import (
	"context"
	"houseflowApi/internal/data/database"
	"reflect"
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

	_, err = mongoCtx.Collection.InsertOne(ctx, entity)
	if err != nil {
		var zero *T
		r.mongoContext.CloseConnection(ctx)
		return zero, err
	}

	r.mongoContext.CloseConnection(ctx)
	return &entity, nil
}
