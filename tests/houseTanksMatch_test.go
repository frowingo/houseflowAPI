package tests

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"houseflowApi/internal/application/game/games/houseTanks"
)

// newTanksMatch starts a match whose players all hold control.
func newTanksMatch(t *testing.T, players int) *houseTanks.Match {
	t.Helper()
	match, err := houseTanks.NewMatch("session-match", tanksPlayerIDs(players), houseTanks.DefaultMatchTiming())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range tanksPlayerIDs(players) {
		if err := match.SetControl(id, true); err != nil {
			t.Fatal(err)
		}
	}
	return match
}

func advanceTanksMatch(match *houseTanks.Match, ticks int) {
	for range ticks {
		match.Advance()
	}
}

// playTanksRound skips the countdown and destroys every tank but the winner's
// ("" destroys all of them on one tick).
func playTanksRound(t *testing.T, match *houseTanks.Match, winner string) {
	t.Helper()
	for match.RoundPhase == houseTanks.RoundCountdown {
		match.Advance()
	}
	if match.RoundPhase != houseTanks.RoundPlaying {
		t.Fatalf("round phase = %s", match.RoundPhase)
	}
	destroyTanks(match, winner)
	match.Advance()
}

func destroyTanks(match *houseTanks.Match, except ...string) {
	for index := range match.World.Tanks {
		tank := &match.World.Tanks[index]
		spared := false
		for _, id := range except {
			spared = spared || id == tank.PlayerID
		}
		if !spared {
			tank.Active, tank.Armor = false, 0
		}
	}
}

func readyAll(t *testing.T, match *houseTanks.Match, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := match.SetReady(id, true); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHouseTanksMatchStart(t *testing.T) {
	for _, invalid := range [][]string{nil, {"seat0"}, tanksPlayerIDs(9), {"a", "a"}, {"a", ""}} {
		if _, err := houseTanks.NewMatch("session", invalid, houseTanks.DefaultMatchTiming()); err == nil {
			t.Fatalf("roster %v accepted", invalid)
		}
	}
	if _, err := houseTanks.NewMatch("", tanksPlayerIDs(2), houseTanks.DefaultMatchTiming()); err == nil {
		t.Fatal("empty session accepted")
	}
	if _, err := houseTanks.NewMatch("session", tanksPlayerIDs(2), houseTanks.MatchTiming{}); err == nil {
		t.Fatal("zero timing accepted")
	}
	if timing := houseTanks.DefaultMatchTiming(); timing.InsufficientGraceTicks != 1200 || timing.PresenceGraceTicks != 1920 {
		t.Fatalf("default timing = %+v", timing)
	}
	match, err := houseTanks.NewMatch("session-start", tanksPlayerIDs(6), houseTanks.DefaultMatchTiming())
	if err != nil {
		t.Fatal(err)
	}
	random := houseTanks.NewRandom(houseTanks.MatchSeed("session-start"))
	expected, _ := houseTanks.SelectMap(&random, 6)
	if match.Seed != houseTanks.MatchSeed("session-start") || match.MapID != expected.ID || match.MapID != houseTanks.MapBasement || match.MapVersion != expected.Version {
		t.Fatal("the session decides the map")
	}
	if match.RoundPhase != houseTanks.RoundCountdown || match.RoundNumber != 1 || match.RoundCountdownEndsAtTick != houseTanks.RoundCountdownTicks || match.Tick != 0 {
		t.Fatalf("round 1 = %+v", match)
	}
	// Until a control binds, every tank is a stationary target.
	for _, tank := range match.World.Tanks {
		if tank.Controllable {
			t.Fatal("unbound tank is controllable")
		}
	}
	if houseTanks.RoundCountdownTicks != 3*120 || houseTanks.RoundReadyWindowTicks != 30*120 || houseTanks.RoundReadyIdleTicks != 120*120 ||
		houseTanks.MaximumMatchTicks != 5*60*120 || houseTanks.EventWindowTicks != 120 {
		t.Fatal("round timers must follow the match durations")
	}
}

func TestHouseTanksMatchCountdown(t *testing.T) {
	match := newTanksMatch(t, 2)
	before := match.World.Tanks[0]
	for match.Tick < houseTanks.RoundCountdownTicks-1 {
		match.Advance()
		if match.World.Tick != match.Tick || match.RoundPhase != houseTanks.RoundCountdown || match.World.Jokers != nil {
			t.Fatal("countdown keeps the world still and in step with the match")
		}
	}
	if !reflect.DeepEqual(before, match.World.Tanks[0]) {
		t.Fatal("tanks moved during the countdown")
	}
	match.Advance()
	if match.RoundPhase != houseTanks.RoundPlaying || !match.Playing() || match.World.Jokers == nil {
		t.Fatal("round must start when the countdown ends")
	}
	due := match.World.Jokers.NextTokenTick - match.Tick
	if due < houseTanks.PowerUpMinimumTicks || due > houseTanks.PowerUpMaximumTicks {
		t.Fatalf("first joker due %d ticks after the round starts", due)
	}
	match.Advance()
	if match.World.Tick != match.Tick || match.World.Tanks[0].Rotation == before.Rotation {
		t.Fatal("playing steps the world every tick")
	}
	if err := match.SetReady("seat0", true); !errors.Is(err, houseTanks.ErrNotRoundReady) {
		t.Fatalf("vote while playing: %v", err)
	}
}

func TestHouseTanksMatchRoundOutcomes(t *testing.T) {
	match := newTanksMatch(t, 3)
	// A stationary target is a live tank: the round goes on without it moving.
	for match.RoundPhase == houseTanks.RoundCountdown {
		match.Advance()
	}
	_ = match.SetControl("seat2", false)
	destroyTanks(match, "seat0", "seat2")
	advanceTanksMatch(match, 240)
	if match.RoundPhase != houseTanks.RoundPlaying || match.RoundsPlayed != 0 {
		t.Fatal("the round ends only when the stationary target is destroyed too")
	}
	_ = match.SetControl("seat2", true)
	jokers := *match.World.Jokers
	destroyTanks(match, "seat0")
	match.Advance()
	if match.RoundPhase != houseTanks.RoundReady || match.RoundsPlayed != 1 || match.RoundWinnerID != "seat0" || match.Players[0].RoundsWon != 1 {
		t.Fatalf("win = %+v", match)
	}
	last := match.Events[len(match.Events)-1]
	if last.Kind != houseTanks.EventRoundEnded || last.RoundNumber != 1 || last.WinnerID != "seat0" || last.Tick != match.Tick {
		t.Fatalf("roundEnded event = %+v", last)
	}
	world := match.World
	if len(world.Projectiles) != 0 || world.Token != nil || world.Jokers != nil || world.Tanks[0].Driving || world.Tanks[0].PowerUp != nil {
		t.Fatal("a finished round freezes the arena")
	}
	if match.Random != jokers.Random || !reflect.DeepEqual(match.Bag, jokers.Bag) {
		t.Fatal("the joker dealer returns to the match")
	}
	rotation := world.Tanks[0].Rotation
	advanceTanksMatch(match, 60)
	if match.World.Tanks[0].Rotation != rotation {
		t.Fatal("a frozen arena is not stepped")
	}
	// Last tanks destroyed on one tick: a draw, counted but scoring nobody.
	readyAll(t, match, "seat0", "seat1", "seat2")
	match.Advance()
	if match.RoundNumber != 2 || match.RoundPhase != houseTanks.RoundCountdown {
		t.Fatal("everyone ready starts the next round")
	}
	playTanksRound(t, match, "")
	if match.RoundPhase != houseTanks.RoundReady || match.RoundsPlayed != 2 || match.RoundWinnerID != "" ||
		match.Players[0].RoundsWon != 1 || match.Players[1].RoundsWon != 0 || match.Players[2].RoundsWon != 0 {
		t.Fatalf("draw = %+v", match.Players)
	}
	if last := match.Events[len(match.Events)-1]; last.Kind != houseTanks.EventRoundEnded || last.WinnerID != "" || last.RoundNumber != 2 {
		t.Fatalf("draw event = %+v", last)
	}
}

func TestHouseTanksMatchNewRoundResets(t *testing.T) {
	match := newTanksMatch(t, 3)
	for match.RoundPhase == houseTanks.RoundCountdown {
		match.Advance()
	}
	// Damage, a broken crate, a joker and some motion in round 1.
	match.World.Tanks[0].Armor = 2
	match.World.Press("seat0")
	advanceTanksMatch(match, 30)
	for index, obstacle := range match.Definition().Obstacles {
		if obstacle.Style == houseTanks.StyleCrate {
			match.World.Obstacles[index].Destroyed = true
		}
	}
	match.World.Grant("seat1", houseTanks.PowerUpShield)
	destroyTanks(match, "seat0")
	match.Advance()
	bag, random := match.Bag, match.Random
	readyAll(t, match, "seat0", "seat1", "seat2")
	match.Advance()
	fresh, _ := houseTanks.NewWorld(match.Definition(), tanksPlayerIDs(3), match.Tick)
	if !reflect.DeepEqual(match.World.Tanks, fresh.Tanks) || !reflect.DeepEqual(match.World.Obstacles, fresh.Obstacles) ||
		len(match.World.Projectiles) != 0 || match.World.Token != nil {
		t.Fatal("a new round lays out a fresh arena")
	}
	for match.RoundPhase == houseTanks.RoundCountdown {
		match.Advance()
	}
	if !reflect.DeepEqual(match.World.Jokers.Bag, bag) || match.World.Jokers.Random == random {
		t.Fatal("the joker bag carries on and the generator draws the first interval")
	}
	for _, player := range match.Players {
		if player.RoundReady {
			t.Fatal("votes are collected afresh every round")
		}
	}
}

func TestHouseTanksMatchReadyRules(t *testing.T) {
	enterReady := func(players int) *houseTanks.Match {
		match := newTanksMatch(t, players)
		playTanksRound(t, match, "seat0")
		if match.RoundPhase != houseTanks.RoundReady {
			t.Fatal("round 1 must end")
		}
		return match
	}
	// Two of three ready open a 30 s window; the third starts it at once.
	match := enterReady(3)
	readyAll(t, match, "seat1", "seat2")
	match.Advance()
	if match.ReadyWindowEndsAtTick == nil || *match.ReadyWindowEndsAtTick != match.Tick+houseTanks.RoundReadyWindowTicks || match.RoundPhase != houseTanks.RoundReady {
		t.Fatal("two ready players open the window")
	}
	readyAll(t, match, "seat0")
	match.Advance()
	if match.RoundPhase != houseTanks.RoundCountdown || match.RoundNumber != 2 {
		t.Fatal("everyone ready inside the window starts at once")
	}
	// Dropping below two closes the window; the idle clock restarts.
	match = enterReady(3)
	readyAll(t, match, "seat0", "seat1")
	match.Advance()
	_ = match.SetReady("seat1", false)
	match.Advance()
	if match.ReadyWindowEndsAtTick != nil || match.ReadyIdleSinceTick != match.Tick {
		t.Fatal("window must close below two ready players")
	}
	// A window that runs out starts the round for every controlled player.
	readyAll(t, match, "seat0", "seat1")
	match.Advance()
	endsAt := *match.ReadyWindowEndsAtTick
	for match.Tick < endsAt-1 {
		match.Advance()
	}
	if match.RoundPhase != houseTanks.RoundReady {
		t.Fatal("window ended early")
	}
	match.Advance()
	if match.RoundPhase != houseTanks.RoundCountdown || !match.World.Tanks[2].Controllable {
		t.Fatal("players who did not vote play too")
	}
	// Only controlled players count; a disconnected tank becomes a target.
	match = enterReady(3)
	_ = match.SetControl("seat2", false)
	readyAll(t, match, "seat0", "seat1")
	match.Advance()
	if match.RoundPhase != houseTanks.RoundCountdown || match.World.Tanks[2].Controllable {
		t.Fatal("all controlled players ready start the round; the absent tank is a stationary target")
	}
	if err := match.SetReady("seat0", true); !errors.Is(err, houseTanks.ErrNotRoundReady) {
		t.Fatalf("vote in countdown: %v", err)
	}
	// The destroyed tank's owner votes; an uncontrolled player cannot.
	match = enterReady(2)
	if err := match.SetReady("seat1", true); err != nil {
		t.Fatalf("destroyed tank's owner must vote: %v", err)
	}
	_ = match.SetControl("seat0", false)
	if err := match.SetReady("seat0", true); !errors.Is(err, houseTanks.ErrControlRequired) {
		t.Fatalf("uncontrolled vote: %v", err)
	}
	if err := match.SetReady("stranger", true); !errors.Is(err, houseTanks.ErrControlRequired) {
		t.Fatalf("stranger vote: %v", err)
	}
	// A single ready player never starts a round alone.
	match = enterReady(2)
	readyAll(t, match, "seat0")
	advanceTanksMatch(match, 600)
	if match.RoundPhase != houseTanks.RoundReady || match.ReadyWindowEndsAtTick != nil {
		t.Fatal("one ready player is not enough")
	}
	// Two minutes without an open window end the match.
	idleSince := match.ReadyIdleSinceTick
	for match.Tick < idleSince+houseTanks.RoundReadyIdleTicks-1 {
		match.Advance()
	}
	if match.Ended() {
		t.Fatal("ended before the idle limit")
	}
	match.Advance()
	if match.Status != houseTanks.ResultCancelled || match.EndReason != houseTanks.EndSessionExpired || match.ChampionID != "" || match.Players[0].RoundsWon != 1 {
		t.Fatalf("idle end = %s %s", match.Status, match.EndReason)
	}
}

func TestHouseTanksMatchChampion(t *testing.T) {
	match := newTanksMatch(t, 2)
	for round := 1; round <= houseTanks.RoundWinsNeeded; round++ {
		winner := "seat1"
		if round != 2 {
			winner = "seat0"
		}
		playTanksRound(t, match, winner)
		if match.Ended() {
			break
		}
		readyAll(t, match, "seat0", "seat1")
		match.Advance()
	}
	if match.Ended() {
		t.Fatal("two wins are not enough")
	}
	playTanksRound(t, match, "seat0")
	if match.Status != houseTanks.ResultCompleted || match.EndReason != houseTanks.EndChampionDecided || match.ChampionID != "seat0" ||
		match.RoundPhase != houseTanks.MatchEnded || match.RoundsPlayed != 4 || match.Players[0].RoundsWon != 3 || match.Players[1].RoundsWon != 1 {
		t.Fatalf("champion = %+v", match)
	}
	tick := match.Tick
	match.Advance()
	if match.Tick != tick || !errors.Is(match.Cancel(houseTanks.EndCancelledByUser), houseTanks.ErrMatchEnded) || !errors.Is(match.SetReady("seat0", true), houseTanks.ErrMatchEnded) {
		t.Fatal("an ended match is final")
	}
}

func TestHouseTanksMatchPresence(t *testing.T) {
	grace := houseTanks.DefaultMatchTiming().InsufficientGraceTicks
	// One player gone for the reconnect grace ends the match with its scores.
	match := newTanksMatch(t, 2)
	playTanksRound(t, match, "seat1")
	advanceTanksMatch(match, int(houseTanks.DefaultMatchTiming().PresenceGraceTicks))
	_ = match.SetControl("seat0", false)
	match.Advance()
	since := *match.InsufficientSinceTick
	for match.Tick < since+grace-1 {
		match.Advance()
	}
	if match.Ended() {
		t.Fatal("ended inside the grace")
	}
	match.Advance()
	if match.Status != houseTanks.ResultCancelled || match.EndReason != houseTanks.EndInsufficientPlayers || match.Players[1].RoundsWon != 1 || match.ChampionID != "" {
		t.Fatalf("insufficient = %s %s", match.Status, match.EndReason)
	}
	// Coming back inside the grace resets the clock, in any phase.
	match = newTanksMatch(t, 2)
	advanceTanksMatch(match, 2000)
	_ = match.SetControl("seat1", false)
	advanceTanksMatch(match, int(grace-1))
	if match.InsufficientSinceTick == nil {
		t.Fatal("one present player starts the clock")
	}
	_ = match.SetControl("seat1", true)
	match.Advance()
	if match.Ended() || match.InsufficientSinceTick != nil {
		t.Fatal("reconnecting must cancel the countdown to the end")
	}
	// Nobody bound yet: the start grace covers control timeout + reconnect.
	match, _ = houseTanks.NewMatch("session-unbound", tanksPlayerIDs(3), houseTanks.DefaultMatchTiming())
	for !match.Ended() {
		match.Advance()
	}
	if match.Tick != houseTanks.DefaultMatchTiming().PresenceGraceTicks+grace || match.EndReason != houseTanks.EndInsufficientPlayers {
		t.Fatalf("unbound match ended at %d with %s", match.Tick, match.EndReason)
	}
	// A recovery extends the grace the same way.
	match = newTanksMatch(t, 2)
	advanceTanksMatch(match, 2000)
	_ = match.SetControl("seat0", false)
	_ = match.SetControl("seat1", false)
	match.AwaitPlayers(match.Tick + 600)
	advanceTanksMatch(match, 599)
	if match.InsufficientSinceTick != nil {
		t.Fatal("players count as present while a recovery waits for them")
	}
	match.Advance()
	if match.InsufficientSinceTick == nil {
		t.Fatal("the recovery grace runs out")
	}
	// Leaving is permanent; with fewer than two players left it ends at once.
	match = newTanksMatch(t, 3)
	_ = match.Leave("seat2")
	if err := match.SetControl("seat2", true); !errors.Is(err, houseTanks.ErrControlRequired) {
		t.Fatalf("left player rebound: %v", err)
	}
	match.Advance()
	if match.Ended() || match.World.Tanks[2].Controllable || !match.Players[2].HasLeft {
		t.Fatal("two players are still enough; the left tank stays a target")
	}
	_ = match.Leave("seat1")
	match.Advance()
	if match.EndReason != houseTanks.EndInsufficientPlayers {
		t.Fatal("one remaining player ends the match at once")
	}
}

func TestHouseTanksMatchLimitsAndCancel(t *testing.T) {
	// Tanks that never meet run into the five-minute cap.
	match := newTanksMatch(t, 2)
	for !match.Ended() {
		match.Advance()
	}
	if match.Tick != houseTanks.MaximumMatchTicks || match.Status != houseTanks.ResultCancelled || match.EndReason != houseTanks.EndSessionExpired ||
		match.RoundPhase != houseTanks.MatchEnded || match.World.Jokers != nil {
		t.Fatalf("cap = tick %d %s %s", match.Tick, match.Status, match.EndReason)
	}
	match = newTanksMatch(t, 2)
	if err := match.Cancel(houseTanks.EndChampionDecided); err == nil {
		t.Fatal("a champion cannot be declared by cancelling")
	}
	if err := match.Cancel(houseTanks.EndCancelledByUser); err != nil || match.Status != houseTanks.ResultCancelled || match.RoundPhase != houseTanks.MatchEnded {
		t.Fatal("cancel")
	}
}

func TestHouseTanksMatchEventWindow(t *testing.T) {
	match := newTanksMatch(t, 2)
	for match.RoundPhase == houseTanks.RoundCountdown {
		match.Advance()
	}
	match.World.Press("seat0")
	match.Advance()
	fired := match.Events[0]
	if fired.Kind != houseTanks.EventFired || fired.Sequence != 1 {
		t.Fatalf("first event = %+v", fired)
	}
	match.World.Release("seat0")
	for range houseTanks.EventWindowTicks - 2 {
		match.Advance()
	}
	if len(match.Events) == 0 || match.Events[0].Sequence != 1 {
		t.Fatal("events stay for a second")
	}
	match.Advance()
	for _, event := range match.Events {
		if event.Sequence == 1 || match.Tick-event.Tick >= houseTanks.EventWindowTicks {
			t.Fatal("older events leave the window")
		}
	}
	previous := int64(0)
	advanceTanksMatch(match, 2000)
	for _, event := range match.Events {
		if event.Sequence <= previous {
			t.Fatal("sequences increase")
		}
		previous = event.Sequence
	}
}

func TestHouseTanksMatchCheckpoint(t *testing.T) {
	match := newTanksMatch(t, 4)
	for match.RoundPhase == houseTanks.RoundCountdown {
		match.Advance()
	}
	match.World.Press("seat0")
	match.World.Press("seat2")
	advanceTanksMatch(match, 1500)
	encoded, err := json.Marshal(match)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := houseTanks.DecodeMatch(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for range 1200 {
		match.Advance()
		restored.Advance()
	}
	left, _ := json.Marshal(match)
	right, _ := json.Marshal(restored)
	if string(left) != string(right) {
		t.Fatal("a restored match continues identically")
	}
	corrupt := func(change func(map[string]any)) []byte {
		var value map[string]any
		_ = json.Unmarshal(encoded, &value)
		change(value)
		data, _ := json.Marshal(value)
		return data
	}
	for name, data := range map[string][]byte{
		"map version": corrupt(func(value map[string]any) { value["mapVersion"] = 99 }),
		"seed":        corrupt(func(value map[string]any) { value["seed"] = 1 }),
		"phase":       corrupt(func(value map[string]any) { value["roundPhase"] = "paused" }),
		"world":       corrupt(func(value map[string]any) { value["world"] = nil }),
		"seats": corrupt(func(value map[string]any) {
			players := value["players"].([]any)
			players[0], players[1] = players[1], players[0]
		}),
		"bag": corrupt(func(value map[string]any) {
			value["bag"] = map[string]any{"queue": []string{"aim", "aim"}, "last": "rocket"}
		}),
		"syntax": []byte("{"),
	} {
		if _, err := houseTanks.DecodeMatch(data); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
