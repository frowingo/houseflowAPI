package tests

import (
	"encoding/json"
	"testing"
	"time"

	"houseflowApi/internal/models/dtos"
)

func TestUTCDateTimeMarshalsAsISO8601UTC(t *testing.T) {
	value := dtos.NewUTCDateTime(time.Date(2026, 7, 12, 3, 4, 5, 123, time.FixedZone("TRT", 3*60*60)))

	bytes, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal UTC datetime: %v", err)
	}

	if string(bytes) != `"2026-07-12T00:04:05Z"` {
		t.Fatalf("expected UTC ISO-8601 output, got %s", string(bytes))
	}
}

func TestUTCDateTimeUnmarshalsISO8601InputAsUTC(t *testing.T) {
	var value dtos.UTCDateTime

	if err := json.Unmarshal([]byte(`"2026-07-12T00:04:05Z"`), &value); err != nil {
		t.Fatalf("unmarshal UTC datetime: %v", err)
	}

	if value.Location() != time.UTC {
		t.Fatalf("expected UTC location, got %s", value.Location())
	}
	if value.Format(time.RFC3339) != "2026-07-12T00:04:05Z" {
		t.Fatalf("expected UTC time to round-trip, got %s", value.Format(time.RFC3339))
	}
}
