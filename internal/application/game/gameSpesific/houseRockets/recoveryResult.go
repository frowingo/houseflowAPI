package houseRockets

import (
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

// Missing state cannot invent a winner, distance, elimination or a fresh spawn.
// Only an explicitly cancelled result can be reconstructed from the frozen roster.
func RecoveryCancellationResult(snapshot gameDomain.SessionSnapshot, reason EndReason, now time.Time) (HouseRocketsResultModel, error) {
	startedAt := snapshot.StartedAt
	result := HouseRocketsResultModel{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, GameKey: GameKey, ProtocolVersion: ProtocolVersion, CourseVersion: CourseVersion, Status: ResultCancelled, EndReason: reason, StartedAt: &startedAt, EndedAt: now.UTC(), Players: []HouseRocketsPlayerResultModel{}}
	for _, player := range snapshot.Players {
		if !player.ReadyAt.IsZero() && (player.LeftAt.IsZero() || !player.LeftAt.Before(snapshot.StartedAt.Add(-snapshot.Rules.CountdownDuration))) {
			result.Players = append(result.Players, HouseRocketsPlayerResultModel{PlayerID: player.PlayerID})
		}
	}
	return result, ValidateResult(result, snapshot)
}
