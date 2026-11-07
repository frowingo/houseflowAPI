package fausSwitch

import (
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

// proposeResult prepares the uncommitted result of a race that has its
// outcome; only the application's result transaction can make it final. The
// end time is never before the start plus the race's duration.
func proposeResult(session RuntimeSession, race *simulation, endedAt time.Time) (FausSwitchResultModel, error) {
	ranked, err := race.result(session.playerIDs())
	if err != nil || endedAt.IsZero() {
		return FausSwitchResultModel{}, ErrInvalidInput
	}
	startedAt := session.StartedAt.UTC()
	endedAt = endedAt.UTC()
	if earliest := startedAt.Add(ticksDuration(ranked.tick)); endedAt.Before(earliest) {
		endedAt = earliest
	}
	return FausSwitchResultModel{SessionID: session.SessionID, HouseID: session.HouseID, GameKey: GameKey, ProtocolVersion: ProtocolVersion,
		CourseVersion: CourseVersion, Status: ranked.status, EndReason: ranked.endReason, WinnerID: ranked.winnerID, StartedAt: &startedAt,
		EndedAt: endedAt, DurationSeconds: float64(ranked.tick) / PhysicsRateHz, Players: ranked.players}, nil
}

// eligible reports whether the session froze the member into the race: ready
// when the countdown started and not gone before it. Equality is ambiguous
// after BSON's millisecond truncation, so strict excludes it and the lenient
// form accepts it.
func eligible(member gameDomain.SessionPlayer, session gameDomain.SessionSnapshot, strict bool) bool {
	if member.ReadyAt.IsZero() {
		return false
	}
	frozenAt := session.StartedAt.Add(-session.Rules.CountdownDuration)
	if strict {
		return member.LeftAt.IsZero() || member.LeftAt.After(frozenAt)
	}
	return member.LeftAt.IsZero() || !member.LeftAt.Before(frozenAt)
}

// RecoveryCancellationResult rebuilds a cancelled result from the frozen
// roster when no race state survives. It never invents a rank, a finish, an
// elimination or a distance.
func RecoveryCancellationResult(session gameDomain.SessionSnapshot, reason EndReason, now time.Time) (FausSwitchResultModel, error) {
	startedAt := session.StartedAt
	result := FausSwitchResultModel{SessionID: session.SessionID, HouseID: session.HouseID, GameKey: GameKey, ProtocolVersion: ProtocolVersion,
		CourseVersion: CourseVersion, Status: ResultCancelled, EndReason: reason, StartedAt: &startedAt, EndedAt: now.UTC(),
		Players: []FausSwitchPlayerResultModel{}}
	for _, player := range session.Players {
		if eligible(player, session, false) {
			result.Players = append(result.Players, FausSwitchPlayerResultModel{PlayerID: player.PlayerID})
		}
	}
	return result, ValidateResult(result, session)
}
