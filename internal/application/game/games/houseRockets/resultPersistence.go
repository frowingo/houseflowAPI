package houseRockets

import (
	"encoding/json"
	gameDomain "houseflowApi/internal/application/game/domain"
	"time"
)

// ResultSchemaVersion versions the stored game payload, not the realtime or
// course protocol. Only this game translates its wire DTO to the shared envelope.
const ResultSchemaVersion = 1

func EncodeMatchResult(result HouseRocketsResultModel) (gameDomain.MatchResult, error) {
	if result.StartedAt == nil {
		return gameDomain.MatchResult{}, ErrInvalidInput
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return gameDomain.MatchResult{}, err
	}
	stored := gameDomain.MatchResult{SessionID: result.SessionID, HouseID: result.HouseID, GameKey: result.GameKey, SchemaVersion: ResultSchemaVersion, Status: gameDomain.MatchResultStatus(result.Status), EndReason: string(result.EndReason), StartedAt: *result.StartedAt, EndedAt: result.EndedAt, Payload: payload}
	return stored, stored.Validate()
}

func DecodeMatchResult(stored gameDomain.MatchResult) (HouseRocketsResultModel, error) {
	if err := stored.Validate(); err != nil {
		return HouseRocketsResultModel{}, err
	}
	if stored.GameKey != GameKey || stored.SchemaVersion != ResultSchemaVersion {
		return HouseRocketsResultModel{}, ErrInvalidInput
	}
	var result HouseRocketsResultModel
	if err := json.Unmarshal(stored.Payload, &result); err != nil {
		return HouseRocketsResultModel{}, err
	}
	if result.SessionID != stored.SessionID || result.HouseID != stored.HouseID || result.GameKey != stored.GameKey || result.ProtocolVersion != ProtocolVersion || result.CourseVersion != CourseVersion || gameDomain.MatchResultStatus(result.Status) != stored.Status || string(result.EndReason) != stored.EndReason || result.StartedAt == nil || !result.StartedAt.Truncate(time.Millisecond).Equal(stored.StartedAt.Truncate(time.Millisecond)) || !result.EndedAt.Truncate(time.Millisecond).Equal(stored.EndedAt.Truncate(time.Millisecond)) {
		return HouseRocketsResultModel{}, ErrInvalidInput
	}
	return result, nil
}
