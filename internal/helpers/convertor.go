package helpers

import (
	"fmt"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func ToMongoId(id string) (primitive.ObjectID, error) {
	if id == "" {
		return primitive.NilObjectID, fmt.Errorf("id cannot be empty")
	}

	if len(id) != 24 {
		return primitive.NilObjectID, fmt.Errorf("invalid ObjectID length: expected 24 characters, got %d", len(id))
	}

	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return primitive.NilObjectID, fmt.Errorf("invalid ObjectID format: %v", err)
	}
	return objectID, nil
}
