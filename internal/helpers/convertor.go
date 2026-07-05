package helpers

import (
	"strconv"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func ToMongoId(id string) (primitive.ObjectID, error) {
	if id == "" {
		return primitive.NilObjectID, NewLocalizedError("common.error.id_cannot_be_empty")
	}

	if len(id) != 24 {
		return primitive.NilObjectID, NewLocalizedError("common.error.invalid_object_id_length", strconv.Itoa(len(id)))
	}

	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return primitive.NilObjectID, NewLocalizedError("common.error.invalid_object_id_format", err.Error())
	}
	return objectID, nil
}
