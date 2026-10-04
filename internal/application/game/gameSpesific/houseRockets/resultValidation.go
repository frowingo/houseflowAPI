package houseRockets

import (
	"math"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

func ValidateResult(result HouseRocketsResultModel, session gameDomain.SessionSnapshot) error {
	if result.SessionID != session.SessionID || result.HouseID != session.HouseID || result.GameKey != GameKey || session.GameKey != GameKey || session.StartedAt.IsZero() || result.ProtocolVersion != ProtocolVersion || result.CourseVersion != CourseVersion || result.StartedAt == nil || !result.StartedAt.Truncate(time.Millisecond).Equal(session.StartedAt.Truncate(time.Millisecond)) || result.EndedAt.IsZero() || !finite(result.DurationSeconds) || result.DurationSeconds < 0 || result.DurationSeconds > float64(MaximumMatchDuration/time.Second) || len(result.Players) < MinimumPlayers || len(result.Players) > MaximumPlayers {
		return ErrInvalidInput
	}
	ticks := int64(math.Round(result.DurationSeconds * PhysicsRateHz))
	if math.Abs(result.DurationSeconds*PhysicsRateHz-float64(ticks)) > 1e-6 || result.EndedAt.Before(result.StartedAt.Add(time.Duration(ticks)*time.Second/PhysicsRateHz)) {
		return ErrInvalidInput
	}
	seen := make(map[string]bool, len(result.Players))
	alive, aliveID := 0, ""
	for _, player := range result.Players {
		if !validID(player.PlayerID) || seen[player.PlayerID] || !finite(player.Distance) || player.Distance < 0 || player.Distance > result.DurationSeconds*BaseSpeed*BoostMultiplier+2*RocketRadius {
			return ErrInvalidInput
		}
		seen[player.PlayerID] = true
		eligible := false
		for _, member := range session.Players {
			if member.PlayerID == player.PlayerID && !member.ReadyAt.IsZero() && (member.LeftAt.IsZero() || !member.LeftAt.Before(session.StartedAt.Add(-session.Rules.CountdownDuration))) {
				eligible = true
			}
		}
		if !eligible {
			return ErrInvalidInput
		}
		if player.EliminatedAtTick == nil {
			if player.EliminationReason != nil {
				return ErrInvalidInput
			}
			alive++
			aliveID = player.PlayerID
		} else {
			if *player.EliminatedAtTick < 0 || *player.EliminatedAtTick > ticks || player.EliminationReason == nil {
				return ErrInvalidInput
			}
			switch *player.EliminationReason {
			case EliminationBehindCamera, EliminationForfeit, EliminationConnectionExpired, EliminationMembershipRevoked:
			default:
				return ErrInvalidInput
			}
		}
	}
	for _, member := range session.Players {
		// Equality is ambiguous after BSON's millisecond truncation: this
		// player may have left just before the frozen roster was selected.
		eligible := !member.ReadyAt.IsZero() && (member.LeftAt.IsZero() || member.LeftAt.After(session.StartedAt.Add(-session.Rules.CountdownDuration)))
		if eligible && !seen[member.PlayerID] {
			return ErrInvalidInput
		}
	}
	if !validOutcome(&SimulationOutcome{Status: result.Status, EndReason: result.EndReason, WinnerID: result.WinnerID}, alive, aliveID, ticks) {
		return ErrInvalidInput
	}
	for _, player := range result.Players {
		if result.Status == ResultCancelled {
			if player.Rank != nil {
				return ErrInvalidInput
			}
			continue
		}
		rank := 1
		if player.EliminatedAtTick != nil {
			for _, other := range result.Players {
				if other.EliminatedAtTick == nil || *other.EliminatedAtTick > *player.EliminatedAtTick {
					rank++
				}
			}
		}
		if player.Rank == nil || *player.Rank != rank {
			return ErrInvalidInput
		}
	}
	return nil
}
