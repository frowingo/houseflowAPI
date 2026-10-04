package houseRockets

import "time"

// ProposeResult prepares an uncommitted result; only the later application transaction can publish it as final.
func (simulation *Simulation) ProposeResult(endedAt time.Time) (HouseRocketsResultModel, error) {
	if simulation.outcome == nil {
		return HouseRocketsResultModel{}, ErrSimulationNotFinished
	}
	minimumEnd := simulation.startedAt.Add(time.Duration(simulation.tick) * time.Second / PhysicsRateHz)
	if endedAt.IsZero() || endedAt.Before(minimumEnd) {
		return HouseRocketsResultModel{}, ErrInvalidInput
	}
	startedAt := simulation.startedAt
	result := HouseRocketsResultModel{SessionID: simulation.sessionID, HouseID: simulation.houseID, GameKey: GameKey, ProtocolVersion: ProtocolVersion,
		CourseVersion: CourseVersion, Status: simulation.outcome.Status, EndReason: simulation.outcome.EndReason, WinnerID: cloneValue(simulation.outcome.WinnerID),
		StartedAt: &startedAt, EndedAt: endedAt.UTC(), DurationSeconds: float64(simulation.tick) / PhysicsRateHz, Players: make([]HouseRocketsPlayerResultModel, len(simulation.players))}
	for index, player := range simulation.players {
		entry := HouseRocketsPlayerResultModel{PlayerID: player.state.PlayerID, EliminatedAtTick: cloneValue(player.state.EliminatedAtTick),
			EliminationReason: cloneValue(player.state.EliminationReason), Distance: player.state.Distance()}
		if result.Status == ResultCompleted {
			rank := 1
			if player.state.EliminatedAtTick != nil {
				for _, other := range simulation.players {
					if other.state.IsAlive || *other.state.EliminatedAtTick > *player.state.EliminatedAtTick {
						rank++
					}
				}
			}
			entry.Rank = &rank
		}
		result.Players[index] = entry
	}
	return result, nil
}
