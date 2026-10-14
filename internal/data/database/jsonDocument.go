package database

import (
	"bytes"
	"encoding/json"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
)

// jsonDocument stores a JSON object as a readable subdocument. Integers stay
// integers so operators can edit the values in place.
func jsonDocument(raw json.RawMessage) (bson.M, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	object, ok := documentValue(value).(map[string]any)
	if !ok {
		return nil, errors.New("JSON object is required")
	}
	return bson.M(object), nil
}

func documentValue(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer
		}
		float, _ := typed.Float64()
		return float
	case map[string]any:
		for key, item := range typed {
			typed[key] = documentValue(item)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = documentValue(item)
		}
		return typed
	default:
		return value
	}
}

// documentJSON reads a subdocument back as JSON with sorted keys; whole
// doubles written by a shell decode as integers.
func documentJSON(document bson.M) json.RawMessage {
	if document == nil {
		return nil
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil
	}
	return encoded
}
