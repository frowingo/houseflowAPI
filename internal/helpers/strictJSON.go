package helpers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DecodeStrictObject decodes one JSON object and rejects duplicate keys,
// unknown fields, missing or null required fields and trailing values.
func DecodeStrictObject(data []byte, target any, optionalFields []string, requiredFields ...string) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("JSON object is required")
	}
	for _, field := range requiredFields {
		value, exists := fields[field]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s is required", field)
		}
	}
	allowedFields := make(map[string]bool, len(requiredFields)+len(optionalFields))
	for _, field := range requiredFields {
		allowedFields[field] = true
	}
	for _, field := range optionalFields {
		allowedFields[field] = true
	}
	for field := range fields {
		if !allowedFields[field] {
			return fmt.Errorf("unknown field %s", field)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return EnsureJSONEnd(decoder)
}

// EnsureJSONEnd rejects any value after the one already decoded.
func EnsureJSONEnd(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values are not allowed")
	}
	return err
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var readValue func() error
	readValue = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, composite := token.(json.Delim)
		if !composite {
			return nil
		}
		seen := make(map[string]bool)
		for decoder.More() {
			if delimiter == '{' {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errors.New("duplicate or invalid JSON field")
				}
				seen[key] = true
			}
			if err := readValue(); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	if err := readValue(); err != nil {
		return err
	}
	return EnsureJSONEnd(decoder)
}

// CanonicalJSONObject re-encodes a JSON object with sorted keys and numbers
// as stored by MongoDB, so equal content always has equal bytes.
func CanonicalJSONObject(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("JSON object is required")
	}
	return json.Marshal(object)
}
