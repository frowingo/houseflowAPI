package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type MatchResultStatus string

const (
	MatchCompleted MatchResultStatus = "completed"
	MatchCancelled MatchResultStatus = "cancelled"
)

var ErrInvalidMatchResult = errors.New("invalid match result")

// MatchResult contains only shared persistence metadata. Each game owns the
// schema and validation of Payload; its exact JSON bytes are immutable.
type MatchResult struct {
	SessionID     string
	HouseID       string
	GameKey       string
	SchemaVersion int
	Status        MatchResultStatus
	EndReason     string
	StartedAt     time.Time
	EndedAt       time.Time
	Payload       json.RawMessage
}

func (result MatchResult) Validate() error {
	payload := bytes.TrimSpace(result.Payload)
	if strings.TrimSpace(result.SessionID) == "" || strings.TrimSpace(result.HouseID) == "" || strings.TrimSpace(result.GameKey) == "" || result.SchemaVersion < 1 || (result.Status != MatchCompleted && result.Status != MatchCancelled) || strings.TrimSpace(result.EndReason) == "" || result.StartedAt.IsZero() || result.EndedAt.Before(result.StartedAt) || len(payload) == 0 || payload[0] != '{' || !json.Valid(payload) {
		return ErrInvalidMatchResult
	}
	return nil
}
