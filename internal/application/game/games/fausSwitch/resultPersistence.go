package fausSwitch

import (
	"encoding/json"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

// ResultSchemaVersion versions the stored payload, not the realtime or
// course protocol.
const ResultSchemaVersion = 1

func EncodeMatchResult(result FausSwitchResultModel) (gameDomain.MatchResult, error) {
	if result.StartedAt == nil {
		return gameDomain.MatchResult{}, ErrInvalidInput
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return gameDomain.MatchResult{}, err
	}
	stored := gameDomain.MatchResult{SessionID: result.SessionID, HouseID: result.HouseID, GameKey: result.GameKey, SchemaVersion: ResultSchemaVersion,
		Status: gameDomain.MatchResultStatus(result.Status), EndReason: string(result.EndReason), StartedAt: *result.StartedAt, EndedAt: result.EndedAt, Payload: payload}
	return stored, stored.Validate()
}

func DecodeMatchResult(stored gameDomain.MatchResult) (FausSwitchResultModel, error) {
	if err := stored.Validate(); err != nil {
		return FausSwitchResultModel{}, err
	}
	if stored.GameKey != GameKey || stored.SchemaVersion != ResultSchemaVersion {
		return FausSwitchResultModel{}, ErrInvalidInput
	}
	var result FausSwitchResultModel
	if err := json.Unmarshal(stored.Payload, &result); err != nil {
		return FausSwitchResultModel{}, err
	}
	sameTime := func(a, b time.Time) bool { return a.Truncate(time.Millisecond).Equal(b.Truncate(time.Millisecond)) }
	if result.SessionID != stored.SessionID || result.HouseID != stored.HouseID || result.GameKey != stored.GameKey || result.ProtocolVersion != ProtocolVersion ||
		result.CourseVersion != CourseVersion || gameDomain.MatchResultStatus(result.Status) != stored.Status || string(result.EndReason) != stored.EndReason ||
		result.StartedAt == nil || !sameTime(*result.StartedAt, stored.StartedAt) || !sameTime(result.EndedAt, stored.EndedAt) {
		return FausSwitchResultModel{}, ErrInvalidInput
	}
	return result, nil
}
