package fausSwitch

import (
	"math"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

// ValidateResult checks a result against the stored session and the race
// rules: the roster is the session's frozen roster, every robot's standing is
// one the race can produce, a completed race is ranked exactly as
// FausSwitchRanking ranks it and its winner is the sole rank-one finisher.
func ValidateResult(result FausSwitchResultModel, session gameDomain.SessionSnapshot) error {
	if result.SessionID != session.SessionID || result.HouseID != session.HouseID || result.GameKey != GameKey || session.GameKey != GameKey ||
		session.StartedAt.IsZero() || result.ProtocolVersion != ProtocolVersion || result.CourseVersion != CourseVersion || result.StartedAt == nil ||
		!result.StartedAt.Truncate(time.Millisecond).Equal(session.StartedAt.Truncate(time.Millisecond)) || result.EndedAt.IsZero() ||
		math.IsNaN(result.DurationSeconds) || math.IsInf(result.DurationSeconds, 0) || result.DurationSeconds < 0 ||
		result.DurationSeconds > float64(MaximumMatchTicks)/PhysicsRateHz || len(result.Players) < MinimumPlayers || len(result.Players) > MaximumPlayers {
		return ErrInvalidInput
	}
	ticks := int64(math.Round(result.DurationSeconds * PhysicsRateHz))
	if math.Abs(result.DurationSeconds*PhysicsRateHz-float64(ticks)) > 1e-6 || result.EndedAt.Before(result.StartedAt.Add(ticksDuration(ticks))) {
		return ErrInvalidInput
	}
	length := raceCourse(len(result.Players)).length
	seen := make(map[string]bool, len(result.Players))
	runners := make([]runner, len(result.Players))
	running := 0
	for seat, player := range result.Players {
		if !validID(player.PlayerID) || seen[player.PlayerID] || math.IsNaN(player.Distance) || player.Distance < 0 || !(player.Distance <= length) {
			return ErrInvalidInput
		}
		seen[player.PlayerID] = true
		member := false
		for _, candidate := range session.Players {
			member = member || (candidate.PlayerID == player.PlayerID && eligible(candidate, session, false))
		}
		if !member {
			return ErrInvalidInput
		}
		robot, ok := standingOf(player, ticks)
		if !ok {
			return ErrInvalidInput
		}
		if robot.running() {
			running++
		}
		runners[seat] = robot
	}
	for _, member := range session.Players {
		if eligible(member, session, true) && !seen[member.PlayerID] {
			return ErrInvalidInput
		}
	}
	switch result.Status {
	case ResultCompleted:
		ended := (result.EndReason == EndRaceComplete && running == 0) || (result.EndReason == EndTimeLimit && running > 0 && ticks == MaximumMatchTicks)
		if !ended {
			return ErrInvalidInput
		}
		standings := ranks(runners)
		leaders, winner := 0, -1
		for seat, player := range result.Players {
			if player.Rank == nil || *player.Rank != standings[seat] {
				return ErrInvalidInput
			}
			if standings[seat] == 1 {
				leaders, winner = leaders+1, seat
			}
		}
		if leaders == 1 && runners[winner].status == RunnerFinished {
			if result.WinnerID == nil || *result.WinnerID != result.Players[winner].PlayerID {
				return ErrInvalidInput
			}
		} else if result.WinnerID != nil {
			return ErrInvalidInput
		}
	case ResultCancelled:
		if !result.EndReason.Cancels() || result.WinnerID != nil {
			return ErrInvalidInput
		}
		for _, player := range result.Players {
			if player.Rank != nil {
				return ErrInvalidInput
			}
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

// standingOf is the robot a result entry describes, as far as ranking goes:
// finished (from tick 1), crashed (with a reason) or still running, never
// two of them, and never after the race's last tick.
func standingOf(player FausSwitchPlayerResultModel, ticks int64) (runner, bool) {
	robot := runner{x: player.Distance, status: RunnerRunning}
	switch {
	case player.FinishedAtTick != nil:
		if player.EliminatedAtTick != nil || player.EliminationReason != nil || *player.FinishedAtTick < 1 || *player.FinishedAtTick > ticks {
			return runner{}, false
		}
		robot.status, robot.finishedAtTick = RunnerFinished, *player.FinishedAtTick
	case player.EliminatedAtTick != nil:
		if player.EliminationReason == nil || *player.EliminatedAtTick < 0 || *player.EliminatedAtTick > ticks {
			return runner{}, false
		}
		switch *player.EliminationReason {
		case EliminationBoundary, EliminationLeftBehind, EliminationForfeit, EliminationMembershipRevoked:
		default:
			return runner{}, false
		}
		robot.status, robot.crashedAtTick, robot.crashReason = RunnerCrashed, *player.EliminatedAtTick, *player.EliminationReason
	case player.EliminationReason != nil:
		return runner{}, false
	}
	return robot, true
}
