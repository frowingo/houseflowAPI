package houseTanks

import (
	"encoding/json"
	"math"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

// ResultSchemaVersion versions the stored game payload, not the realtime or
// map protocol.
const ResultSchemaVersion = 1

// maximumRoundsPlayed bounds a stored result: no round is shorter than its
// countdown.
const maximumRoundsPlayed = MaximumMatchTicks/RoundCountdownTicks + 1

// proposeResult prepares the uncommitted result of an ended match; only the
// application's result transaction can publish it as final.
func proposeResult(session RuntimeSession, match *Match, endedAt time.Time) (HouseTanksResultModel, error) {
	if !match.Ended() || endedAt.IsZero() || endedAt.Before(session.StartedAt.Add(ticksDuration(match.Tick))) {
		return HouseTanksResultModel{}, ErrInvalidInput
	}
	return matchResult(session, match, match.Status, match.EndReason, endedAt), nil
}

// matchResult is the result the match would have with the given outcome.
// Ranks follow round wins; players with as many wins share a rank.
func matchResult(session RuntimeSession, match *Match, status ResultStatus, reason EndReason, endedAt time.Time) HouseTanksResultModel {
	startedAt, mapID, mapVersion := session.StartedAt, match.MapID, match.MapVersion
	result := HouseTanksResultModel{SessionID: session.SessionID, HouseID: session.HouseID, GameKey: GameKey, ProtocolVersion: ProtocolVersion, MapID: &mapID,
		MapVersion: &mapVersion, Status: status, EndReason: reason, RoundsPlayed: match.RoundsPlayed, StartedAt: &startedAt, EndedAt: endedAt.UTC(),
		DurationSeconds: float64(match.Tick) / PhysicsRateHz, Players: make([]HouseTanksPlayerResultModel, 0, len(match.Players))}
	if status == ResultCompleted && match.ChampionID != "" {
		champion := match.ChampionID
		result.ChampionID = &champion
	}
	for _, player := range match.Players {
		entry := HouseTanksPlayerResultModel{PlayerID: player.PlayerID, RoundsWon: player.RoundsWon}
		if status == ResultCompleted {
			rank := rankOf(player.RoundsWon, match.Players)
			entry.FinalRank = &rank
		}
		result.Players = append(result.Players, entry)
	}
	return result
}

func rankOf(roundsWon int, players []MatchPlayer) int {
	rank := 1
	for _, other := range players {
		if other.RoundsWon > roundsWon {
			rank++
		}
	}
	return rank
}

// eligible reports whether the session froze the member into the match.
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

// expectedMap is the map a session of this size plays; the seed comes from
// the session, so it can be recomputed from the roster alone.
func expectedMap(sessionID string, players int) (MapDefinition, error) {
	random := NewRandom(MatchSeed(sessionID))
	return SelectMap(&random, players)
}

func ValidateResult(result HouseTanksResultModel, session gameDomain.SessionSnapshot) error {
	if result.SessionID != session.SessionID || result.HouseID != session.HouseID || result.GameKey != GameKey || session.GameKey != GameKey || session.StartedAt.IsZero() ||
		result.ProtocolVersion != ProtocolVersion || result.StartedAt == nil || !result.StartedAt.Truncate(time.Millisecond).Equal(session.StartedAt.Truncate(time.Millisecond)) ||
		result.EndedAt.IsZero() || math.IsNaN(result.DurationSeconds) || math.IsInf(result.DurationSeconds, 0) || result.DurationSeconds < 0 ||
		result.DurationSeconds > float64(MaximumMatchTicks)/PhysicsRateHz || len(result.Players) < MinimumPlayers || len(result.Players) > MaximumPlayers ||
		result.RoundsPlayed < 0 || result.RoundsPlayed > maximumRoundsPlayed {
		return ErrInvalidInput
	}
	ticks := int64(math.Round(result.DurationSeconds * PhysicsRateHz))
	if math.Abs(result.DurationSeconds*PhysicsRateHz-float64(ticks)) > 1e-6 || result.EndedAt.Before(result.StartedAt.Add(ticksDuration(ticks))) {
		return ErrInvalidInput
	}
	if (result.MapID == nil) != (result.MapVersion == nil) {
		return ErrInvalidInput
	}
	if result.MapID != nil {
		expected, err := expectedMap(session.SessionID, len(result.Players))
		if err != nil || *result.MapID != expected.ID || *result.MapVersion < 1 {
			return ErrInvalidInput
		}
	}
	seen := make(map[string]bool, len(result.Players))
	wins, champions := 0, 0
	for _, player := range result.Players {
		if !validID(player.PlayerID) || seen[player.PlayerID] || player.RoundsWon < 0 || player.RoundsWon > RoundWinsNeeded {
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
		wins += player.RoundsWon
		if player.RoundsWon == RoundWinsNeeded {
			champions++
		}
	}
	for _, member := range session.Players {
		// Equality is ambiguous after BSON's millisecond truncation: this
		// player may have left just before the frozen roster was selected.
		if eligible(member, session, true) && !seen[member.PlayerID] {
			return ErrInvalidInput
		}
	}
	if wins > result.RoundsPlayed {
		return ErrInvalidInput
	}
	switch result.Status {
	case ResultCompleted:
		if result.EndReason != EndChampionDecided || result.ChampionID == nil || champions != 1 || result.MapID == nil {
			return ErrInvalidInput
		}
		for _, player := range result.Players {
			if player.PlayerID == *result.ChampionID && player.RoundsWon != RoundWinsNeeded {
				return ErrInvalidInput
			}
			rank := 1
			for _, other := range result.Players {
				if other.RoundsWon > player.RoundsWon {
					rank++
				}
			}
			if player.FinalRank == nil || *player.FinalRank != rank {
				return ErrInvalidInput
			}
		}
	case ResultCancelled:
		if !cancellationReason(result.EndReason) || result.ChampionID != nil || champions != 0 {
			return ErrInvalidInput
		}
		for _, player := range result.Players {
			if player.FinalRank != nil {
				return ErrInvalidInput
			}
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

func cancellationReason(reason EndReason) bool {
	switch reason {
	case EndInsufficientPlayers, EndCancelledByUser, EndSessionExpired, EndRecoveryFailed, EndCoordinationUnavailable, EndRuntimeOverloaded:
		return true
	}
	return false
}

// RecoveryCancellationResult rebuilds a cancelled result from the frozen
// roster when no runtime state survives. It never invents a score; the map
// is recomputed from the session.
func RecoveryCancellationResult(session gameDomain.SessionSnapshot, reason EndReason, now time.Time) (HouseTanksResultModel, error) {
	startedAt := session.StartedAt
	result := HouseTanksResultModel{SessionID: session.SessionID, HouseID: session.HouseID, GameKey: GameKey, ProtocolVersion: ProtocolVersion, Status: ResultCancelled,
		EndReason: reason, StartedAt: &startedAt, EndedAt: now.UTC(), Players: []HouseTanksPlayerResultModel{}}
	for _, player := range session.Players {
		if eligible(player, session, false) {
			result.Players = append(result.Players, HouseTanksPlayerResultModel{PlayerID: player.PlayerID})
		}
	}
	if definition, err := expectedMap(session.SessionID, len(result.Players)); err == nil {
		result.MapID, result.MapVersion = &definition.ID, &definition.Version
	}
	return result, ValidateResult(result, session)
}

func EncodeMatchResult(result HouseTanksResultModel) (gameDomain.MatchResult, error) {
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

func DecodeMatchResult(stored gameDomain.MatchResult) (HouseTanksResultModel, error) {
	if err := stored.Validate(); err != nil {
		return HouseTanksResultModel{}, err
	}
	if stored.GameKey != GameKey || stored.SchemaVersion != ResultSchemaVersion {
		return HouseTanksResultModel{}, ErrInvalidInput
	}
	var result HouseTanksResultModel
	if err := json.Unmarshal(stored.Payload, &result); err != nil {
		return HouseTanksResultModel{}, err
	}
	if result.SessionID != stored.SessionID || result.HouseID != stored.HouseID || result.GameKey != stored.GameKey || result.ProtocolVersion != ProtocolVersion ||
		gameDomain.MatchResultStatus(result.Status) != stored.Status || string(result.EndReason) != stored.EndReason || result.StartedAt == nil ||
		!result.StartedAt.Truncate(time.Millisecond).Equal(stored.StartedAt.Truncate(time.Millisecond)) ||
		!result.EndedAt.Truncate(time.Millisecond).Equal(stored.EndedAt.Truncate(time.Millisecond)) {
		return HouseTanksResultModel{}, ErrInvalidInput
	}
	return result, nil
}
