package houseTanks

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"houseflowApi/internal/application/game/module"
)

// Match is the round flow of one session: a pure state machine advanced one
// physics tick at a time. The runtime reports control changes and ready
// votes; every deadline is a tick, so a restored match continues exactly.
//
// Rounds run roundCountdown → roundPlaying → roundReady (or matchEnded). The
// tick never stops while the session runs; only roundPlaying steps the world.

// Match errors wrap the core's error kinds so the gateway maps them to the
// protocol's codes.
var (
	ErrInvalidMatch    = errors.New("house tanks match is invalid")
	ErrNotRoundReady   = fmt.Errorf("%w: the round is not collecting ready votes", module.ErrInvalidInput)
	ErrControlRequired = module.ErrControlRequired
	ErrMatchEnded      = module.ErrFinished
)

// MatchTiming holds the presence rules taken from the operator settings when
// the match starts, so a settings change never moves a running deadline.
type MatchTiming struct {
	// InsufficientGraceTicks is how long fewer than two present players may
	// last before the match ends.
	InsufficientGraceTicks int64 `json:"insufficientGraceTicks"`
	// PresenceGraceTicks counts every player as present after the start (or a
	// recovery) until controls had a chance to bind.
	PresenceGraceTicks int64 `json:"presenceGraceTicks"`
}

func durationTicks(duration time.Duration) int64 {
	return (int64(duration)*PhysicsRateHz + int64(time.Second) - 1) / int64(time.Second)
}

// DefaultMatchTiming follows the default reconnect grace and control timeout.
func DefaultMatchTiming() MatchTiming {
	return MatchTiming{InsufficientGraceTicks: durationTicks(ReconnectGraceDuration),
		PresenceGraceTicks: durationTicks(ControlTimeout + ReconnectGraceDuration)}
}

func (timing MatchTiming) valid() bool {
	return timing.InsufficientGraceTicks > 0 && timing.PresenceGraceTicks > 0
}

// MatchPlayer is a seat's state for the whole match. Controlled means a live
// control binding: only then does the tank move and the vote count.
type MatchPlayer struct {
	PlayerID   string `json:"playerId"`
	Seat       int    `json:"seat"`
	RoundsWon  int    `json:"roundsWon"`
	Controlled bool   `json:"controlled"`
	HasLeft    bool   `json:"hasLeft"`
	RoundReady bool   `json:"roundReady"`
}

// MatchEvent is a world or round event numbered for the snapshot event
// window; clients drop the sequences they have already seen.
type MatchEvent struct {
	Sequence int64 `json:"sequence"`
	WorldEvent
	RoundNumber int    `json:"roundNumber,omitempty"`
	WinnerID    string `json:"winnerId,omitempty"`
}

type Match struct {
	SessionID  string        `json:"sessionId"`
	Seed       uint64        `json:"seed"`
	MapID      MapID         `json:"mapId"`
	MapVersion int           `json:"mapVersion"`
	Timing     MatchTiming   `json:"timing"`
	Tick       int64         `json:"tick"`
	Players    []MatchPlayer `json:"players"`
	RoundPhase RoundPhase    `json:"roundPhase"`
	// RoundNumber is the current round, from 1; RoundsPlayed counts finished
	// rounds, draws included.
	RoundNumber              int    `json:"roundNumber"`
	RoundsPlayed             int    `json:"roundsPlayed"`
	RoundCountdownEndsAtTick int64  `json:"roundCountdownEndsAtTick"`
	ReadyWindowEndsAtTick    *int64 `json:"readyWindowEndsAtTick"`
	// ReadyIdleSinceTick is when roundReady last had no open window.
	ReadyIdleSinceTick     int64  `json:"readyIdleSinceTick"`
	InsufficientSinceTick  *int64 `json:"insufficientSinceTick"`
	PresenceGraceUntilTick int64  `json:"presenceGraceUntilTick"`
	RoundWinnerID          string `json:"roundWinnerId,omitempty"`
	ChampionID             string `json:"championId,omitempty"`
	// Status and EndReason are empty until the match ends.
	Status    ResultStatus `json:"status,omitempty"`
	EndReason EndReason    `json:"endReason,omitempty"`
	// Random and Bag deal jokers; the world borrows them while a round plays.
	Random            Random       `json:"random"`
	Bag               JokerBag     `json:"bag"`
	World             *World       `json:"world"`
	Events            []MatchEvent `json:"events"`
	NextEventSequence int64        `json:"nextEventSequence"`
	definition        MapDefinition
}

// NewMatch seats the frozen roster in order and opens round 1 with its
// countdown at tick 0. The seed comes from the session, so the map and the
// jokers are the same on every owner.
func NewMatch(sessionID string, playerIDs []string, timing MatchTiming) (*Match, error) {
	if !validID(sessionID) || len(playerIDs) < MinimumPlayers || len(playerIDs) > MaximumPlayers || !timing.valid() {
		return nil, ErrInvalidMatch
	}
	seed := MatchSeed(sessionID)
	random := NewRandom(seed)
	definition, err := SelectMap(&random, len(playerIDs))
	if err != nil {
		return nil, err
	}
	match := &Match{SessionID: sessionID, Seed: seed, MapID: definition.ID, MapVersion: definition.Version, Timing: timing,
		Players: make([]MatchPlayer, 0, len(playerIDs)), PresenceGraceUntilTick: timing.PresenceGraceTicks, Random: random,
		Events: []MatchEvent{}, NextEventSequence: 1, definition: definition}
	for seat, playerID := range playerIDs {
		if !validID(playerID) || match.playerIndex(playerID) >= 0 {
			return nil, ErrInvalidMatch
		}
		match.Players = append(match.Players, MatchPlayer{PlayerID: playerID, Seat: seat})
	}
	if err := match.beginRound(); err != nil {
		return nil, err
	}
	return match, nil
}

func (match *Match) Definition() MapDefinition { return match.definition }

func (match *Match) Ended() bool { return match.Status != "" }

// Playing reports whether presses act on the arena now.
func (match *Match) Playing() bool { return !match.Ended() && match.RoundPhase == RoundPlaying }

func (match *Match) playerIndex(playerID string) int {
	for index, player := range match.Players {
		if player.PlayerID == playerID {
			return index
		}
	}
	return -1
}

func (match *Match) Player(playerID string) (MatchPlayer, bool) {
	if index := match.playerIndex(playerID); index >= 0 {
		return match.Players[index], true
	}
	return MatchPlayer{}, false
}

// SetControl records that a player's control binding appeared or went away.
// Losing control makes the tank a stationary target and drops the vote.
func (match *Match) SetControl(playerID string, controlled bool) error {
	index := match.playerIndex(playerID)
	if index < 0 {
		return ErrInvalidMatch
	}
	player := &match.Players[index]
	if controlled && player.HasLeft {
		return ErrControlRequired
	}
	player.Controlled = controlled
	if !controlled {
		player.RoundReady = false
	}
	match.World.SetControllable(playerID, controlled)
	return nil
}

// Leave marks an explicit leave or a revoked membership: the tank stays a
// stationary target for the rest of the match.
func (match *Match) Leave(playerID string) error {
	index := match.playerIndex(playerID)
	if index < 0 {
		return ErrInvalidMatch
	}
	if err := match.SetControl(playerID, false); err != nil {
		return err
	}
	match.Players[index].HasLeft = true
	return nil
}

// AwaitPlayers counts every player who has not left as present until the
// given tick, as after the start; a recovered runtime uses it while its
// controls bind again.
func (match *Match) AwaitPlayers(untilTick int64) {
	match.PresenceGraceUntilTick = max(match.PresenceGraceUntilTick, untilTick)
}

// SetReady takes a vote for the next round. Any controlled player votes,
// including one whose tank was destroyed this round.
func (match *Match) SetReady(playerID string, ready bool) error {
	if match.Ended() {
		return ErrMatchEnded
	}
	if match.RoundPhase != RoundReady {
		return ErrNotRoundReady
	}
	index := match.playerIndex(playerID)
	if index < 0 || !match.Players[index].Controlled {
		return ErrControlRequired
	}
	match.Players[index].RoundReady = ready
	return nil
}

// Cancel ends the match without a champion, keeping the round scores.
func (match *Match) Cancel(reason EndReason) error {
	if match.Ended() {
		return ErrMatchEnded
	}
	switch reason {
	case EndInsufficientPlayers, EndCancelledByUser, EndSessionExpired, EndRecoveryFailed, EndCoordinationUnavailable, EndRuntimeOverloaded:
		match.end(ResultCancelled, reason)
		return nil
	}
	return ErrInvalidMatch
}

// Advance moves the match one tick forward.
func (match *Match) Advance() {
	if match.Ended() {
		return
	}
	match.Tick++
	switch match.RoundPhase {
	case RoundCountdown:
		match.World.Tick = match.Tick
		if match.Tick >= match.RoundCountdownEndsAtTick {
			match.RoundPhase = RoundPlaying
			match.World.StartJokers(match.Random, match.Bag)
		}
	case RoundPlaying:
		match.World.Step()
		match.record(match.World.DrainEvents())
		match.resolveRound()
	case RoundReady:
		match.collectVotes()
	}
	if !match.Ended() {
		match.checkPresence()
	}
	if !match.Ended() && match.Tick >= MaximumMatchTicks {
		match.end(ResultCancelled, EndSessionExpired)
	}
	match.pruneEvents()
}

// beginRound lays out a fresh arena: crates restored, tanks on their spawn
// points at full armor, no shots or jokers. The joker generator and bag
// carry on from the previous round.
func (match *Match) beginRound() error {
	ids := make([]string, len(match.Players))
	for index, player := range match.Players {
		ids[index] = player.PlayerID
	}
	world, err := NewWorld(match.definition, ids, match.Tick)
	if err != nil {
		return err
	}
	match.World = world
	for index := range match.Players {
		match.Players[index].RoundReady = false
		world.SetControllable(match.Players[index].PlayerID, match.Players[index].Controlled)
	}
	match.RoundNumber++
	match.RoundPhase, match.RoundWinnerID = RoundCountdown, ""
	match.RoundCountdownEndsAtTick, match.ReadyWindowEndsAtTick = match.Tick+RoundCountdownTicks, nil
	return nil
}

// resolveRound ends the round once one tank is left; when the last tanks
// fall on the same tick it is a draw and nobody scores.
func (match *Match) resolveRound() {
	alive := ""
	for _, tank := range match.World.Tanks {
		if !tank.Active {
			continue
		}
		if alive != "" {
			return
		}
		alive = tank.PlayerID
	}
	match.stopRound()
	match.RoundsPlayed++
	match.RoundWinnerID = alive
	match.record([]WorldEvent{{Tick: match.Tick, Kind: EventRoundEnded}})
	match.Events[len(match.Events)-1].RoundNumber, match.Events[len(match.Events)-1].WinnerID = match.RoundNumber, alive
	if alive == "" {
		match.openRoundReady()
		return
	}
	winner := &match.Players[match.playerIndex(alive)]
	winner.RoundsWon++
	if winner.RoundsWon >= RoundWinsNeeded {
		match.ChampionID = alive
		match.end(ResultCompleted, EndChampionDecided)
		return
	}
	match.openRoundReady()
}

// stopRound freezes the arena and takes the joker dealer back from it.
func (match *Match) stopRound() {
	if match.World == nil {
		return
	}
	if jokers := match.World.Jokers; jokers != nil {
		match.Random, match.Bag = jokers.Random, jokers.Bag
	}
	match.World.Freeze()
}

func (match *Match) openRoundReady() {
	match.RoundPhase, match.ReadyWindowEndsAtTick, match.ReadyIdleSinceTick = RoundReady, nil, match.Tick
	for index := range match.Players {
		match.Players[index].RoundReady = false
	}
}

// collectVotes applies the lobby's ready rule to the next round: everyone
// controlled is ready → start; two ready open a window, which closes below
// two and starts the round when it runs out. A phase in which no window
// opens for too long ends the match.
func (match *Match) collectVotes() {
	controlled, ready := 0, 0
	for _, player := range match.Players {
		if player.Controlled {
			controlled++
			if player.RoundReady {
				ready++
			}
		}
	}
	window := match.ReadyWindowEndsAtTick
	switch {
	case ready >= MinimumPlayers && ready == controlled:
		_ = match.beginRound()
	case window != nil && ready < MinimumPlayers:
		match.ReadyWindowEndsAtTick, match.ReadyIdleSinceTick = nil, match.Tick
	case window != nil && match.Tick >= *window:
		_ = match.beginRound()
	case window == nil && ready >= MinimumPlayers:
		endsAt := match.Tick + RoundReadyWindowTicks
		match.ReadyWindowEndsAtTick = &endsAt
	case window == nil && match.Tick-match.ReadyIdleSinceTick >= RoundReadyIdleTicks:
		match.end(ResultCancelled, EndSessionExpired)
	}
}

// checkPresence ends the match when fewer than two players stay present for
// the grace period, or at once when fewer than two have not left.
func (match *Match) checkPresence() {
	present, remaining := 0, 0
	for _, player := range match.Players {
		if player.HasLeft {
			continue
		}
		remaining++
		if player.Controlled || match.Tick < match.PresenceGraceUntilTick {
			present++
		}
	}
	if remaining < MinimumPlayers {
		match.end(ResultCancelled, EndInsufficientPlayers)
		return
	}
	if present >= MinimumPlayers {
		match.InsufficientSinceTick = nil
		return
	}
	if match.InsufficientSinceTick == nil {
		since := match.Tick
		match.InsufficientSinceTick = &since
	}
	if match.Tick-*match.InsufficientSinceTick >= match.Timing.InsufficientGraceTicks {
		match.end(ResultCancelled, EndInsufficientPlayers)
	}
}

func (match *Match) end(status ResultStatus, reason EndReason) {
	if match.RoundPhase == RoundPlaying || match.RoundPhase == RoundCountdown {
		match.stopRound()
	}
	match.Status, match.EndReason, match.RoundPhase, match.ReadyWindowEndsAtTick = status, reason, MatchEnded, nil
	if status != ResultCompleted {
		match.ChampionID = ""
	}
}

func (match *Match) record(events []WorldEvent) {
	for _, event := range events {
		match.Events = append(match.Events, MatchEvent{Sequence: match.NextEventSequence, WorldEvent: event})
		match.NextEventSequence++
	}
}

// pruneEvents keeps the last second of events.
func (match *Match) pruneEvents() {
	keep := 0
	for keep < len(match.Events) && match.Tick-match.Events[keep].Tick >= EventWindowTicks {
		keep++
	}
	if keep > 0 {
		match.Events = append([]MatchEvent{}, match.Events[keep:]...)
	}
}

// DecodeMatch restores a match stored as JSON and binds it to the map it was
// played on; a catalog without that map version cannot continue it.
func DecodeMatch(data []byte) (*Match, error) {
	var match Match
	if err := json.Unmarshal(data, &match); err != nil {
		return nil, errors.Join(ErrInvalidMatch, err)
	}
	definition, found := MapByID(match.MapID)
	if !found || definition.Version != match.MapVersion || match.Seed != MatchSeed(match.SessionID) || !match.Timing.valid() ||
		len(match.Players) < MinimumPlayers || len(match.Players) > MaximumPlayers || match.RoundNumber < 1 || match.World == nil ||
		match.World.MapID != match.MapID || match.World.MapVersion != match.MapVersion || !match.Bag.Valid() || match.Events == nil {
		return nil, ErrInvalidMatch
	}
	switch match.RoundPhase {
	case RoundCountdown, RoundPlaying, RoundReady, MatchEnded:
	default:
		return nil, ErrInvalidMatch
	}
	if len(match.World.Tanks) != len(match.Players) || len(match.World.Obstacles) != len(definition.Obstacles) {
		return nil, ErrInvalidMatch
	}
	for seat, player := range match.Players {
		tank := match.World.Tanks[seat]
		if player.Seat != seat || tank.Seat != seat || tank.PlayerID != player.PlayerID || !validID(player.PlayerID) {
			return nil, ErrInvalidMatch
		}
	}
	for index, obstacle := range match.World.Obstacles {
		if obstacle.Index != index {
			return nil, ErrInvalidMatch
		}
	}
	if jokers := match.World.Jokers; jokers != nil && !jokers.Bag.Valid() {
		return nil, ErrInvalidMatch
	}
	match.definition, match.World.definition = definition, definition
	return &match, nil
}
