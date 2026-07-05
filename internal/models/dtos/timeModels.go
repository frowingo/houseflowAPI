package dtos

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const iso8601UTCLayout = "2006-01-02T15:04:05Z"

type UTCDateTime struct {
	time.Time
}

func NewUTCDateTime(t time.Time) UTCDateTime {
	return UTCDateTime{Time: t.UTC()}
}

func (t UTCDateTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.UTC().Format(iso8601UTCLayout))
}

func (t *UTCDateTime) UnmarshalJSON(data []byte) error {
	value := strings.TrimSpace(string(data))
	if value == "null" || value == `""` {
		t.Time = time.Time{}
		return nil
	}

	parsed, err := time.Parse(time.RFC3339Nano, strings.Trim(value, `"`))
	if err != nil {
		return fmt.Errorf("common.error.invalid_utc_datetime||%s", value)
	}

	t.Time = parsed.UTC()
	return nil
}
