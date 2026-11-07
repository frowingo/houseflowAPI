package fausSwitch

import "errors"

var errRaceNotOver = errors.New("faus switch: the race has no outcome yet")

// raceResult is the simulation's part of a result; the runtime adds the
// session, house and times. Players are in seat order.
type raceResult struct {
	status    ResultStatus
	endReason EndReason
	// tick is where the race ended: the duration is seconds(tick).
	tick     int64
	winnerID *string
	players  []FausSwitchPlayerResultModel
}

// result ranks the robots as they stand at the end (FausSwitchRanking): only
// a completed race has ranks, and its winner is the sole rank-one robot if it
// finished. Distance is the robot's last course x.
func (race *simulation) result(playerIDs []string) (raceResult, error) {
	if race.outcome == nil {
		return raceResult{}, errRaceNotOver
	}
	if len(playerIDs) != race.count {
		return raceResult{}, errInvalidRace
	}
	runners := race.runners()
	result := raceResult{status: race.outcome.status, endReason: race.outcome.endReason, tick: race.outcome.tick,
		players: make([]FausSwitchPlayerResultModel, race.count)}
	completed := result.status == ResultCompleted
	var standings []int
	if completed {
		standings = ranks(runners)
	}
	leaders := 0
	for seat := range runners {
		state := runnerStateOf(&runners[seat])
		player := FausSwitchPlayerResultModel{PlayerID: playerIDs[seat], FinishedAtTick: state.FinishedAtTick,
			EliminatedAtTick: state.EliminatedAtTick, EliminationReason: state.EliminationReason, Distance: state.X}
		if completed {
			rank := standings[seat]
			player.Rank = &rank
			if rank == 1 {
				leaders++
			}
		}
		result.players[seat] = player
	}
	for seat := range runners {
		if completed && leaders == 1 && standings[seat] == 1 && runners[seat].status == RunnerFinished {
			winner := playerIDs[seat]
			result.winnerID = &winner
		}
	}
	return result, nil
}
