package tests

import (
	"errors"
	"testing"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

func TestGameSessionReadyWindowCountdownAndStart(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	session := newGameSession(t, start, gameDomain.SessionRules{
		MinimumPlayers:      2,
		MaximumPlayers:      4,
		ReadyWindowDuration: 30 * time.Second,
		CountdownDuration:   3 * time.Second,
	})
	session.ClearPendingEvents()

	for index, playerID := range []string{"player-a", "player-b", "player-c"} {
		if err := session.JoinPlayer(playerID, start.Add(time.Duration(index+1)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.SetReady("player-a", true, start.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.State() != gameDomain.SessionLobby {
		t.Fatalf("state = %s, want lobby before minimum ready count", session.State())
	}
	if err := session.SetReady("player-b", true, start.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.State != gameDomain.SessionReadyWindow {
		t.Fatalf("state = %s, want readyWindow", snapshot.State)
	}
	wantReadyDeadline := start.Add(36 * time.Second)
	if !snapshot.ReadyWindowEndsAt.Equal(wantReadyDeadline) {
		t.Fatalf("ready deadline = %s, want %s", snapshot.ReadyWindowEndsAt, wantReadyDeadline)
	}

	if err := session.SetReady("player-c", true, start.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.Advance(wantReadyDeadline.Add(-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if session.State() != gameDomain.SessionReadyWindow {
		t.Fatalf("state advanced before deadline: %s", session.State())
	}
	if err := session.Advance(wantReadyDeadline); err != nil {
		t.Fatal(err)
	}
	if session.State() != gameDomain.SessionCountdown {
		t.Fatalf("state = %s, want countdown", session.State())
	}
	if err := session.Advance(wantReadyDeadline.Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.State() != gameDomain.SessionRunning {
		t.Fatalf("state = %s, want running", session.State())
	}
	for _, playerID := range []string{"player-a", "player-b", "player-c"} {
		player, exists := session.Player(playerID)
		if !exists || player.State != gameDomain.PlayerPlaying {
			t.Fatalf("player %s = %+v, exists=%v; want playing", playerID, player, exists)
		}
	}

	events := session.PendingEvents()
	assertStrictEventVersions(t, events)
	assertEventTypePresent(t, events, gameDomain.ReadyWindowStarted)
	assertEventTypePresent(t, events, gameDomain.CountdownStarted)
	assertEventTypePresent(t, events, gameDomain.GameStarted)
}

func TestGameSessionExcludesPlayersWhoAreNotReady(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	session := newGameSession(t, start, gameDomain.SessionRules{
		MinimumPlayers:      2,
		MaximumPlayers:      4,
		ReadyWindowDuration: 30 * time.Second,
		CountdownDuration:   3 * time.Second,
	})
	session.ClearPendingEvents()
	for _, playerID := range []string{"player-a", "player-b", "player-c"} {
		if err := session.JoinPlayer(playerID, start.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.SetReady("player-a", true, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-b", true, start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.Advance(start.Add(33 * time.Second)); err != nil {
		t.Fatal(err)
	}

	excluded, exists := session.Player("player-c")
	if !exists || excluded.State != gameDomain.PlayerLeft {
		t.Fatalf("excluded player = %+v, exists=%v; want left", excluded, exists)
	}
	assertEventTypePresent(t, session.PendingEvents(), gameDomain.PlayerExcluded)
}

func TestGameSessionReadyWindowReturnsToLobbyIfThresholdIsLost(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	session := newGameSession(t, start, gameDomain.SessionRules{
		MinimumPlayers:      2,
		MaximumPlayers:      4,
		ReadyWindowDuration: 30 * time.Second,
		CountdownDuration:   3 * time.Second,
	})
	if err := session.JoinPlayer("player-a", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.JoinPlayer("player-b", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-a", true, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-b", true, start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-b", false, start.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}

	snapshot := session.Snapshot()
	if snapshot.State != gameDomain.SessionLobby {
		t.Fatalf("state = %s, want lobby", snapshot.State)
	}
	if !snapshot.ReadyWindowEndsAt.IsZero() {
		t.Fatalf("ready deadline was not cleared: %s", snapshot.ReadyWindowEndsAt)
	}
	assertEventTypePresent(t, session.PendingEvents(), gameDomain.ReadyWindowCancelled)
}

func TestGameSessionStartsCountdownImmediatelyAtCapacity(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	session := newGameSession(t, start, gameDomain.SessionRules{
		MinimumPlayers:      2,
		MaximumPlayers:      2,
		ReadyWindowDuration: 30 * time.Second,
		CountdownDuration:   3 * time.Second,
	})
	if err := session.JoinPlayer("player-a", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.JoinPlayer("player-b", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-a", true, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-b", true, start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	snapshot := session.Snapshot()
	if snapshot.State != gameDomain.SessionCountdown {
		t.Fatalf("state = %s, want countdown at maximum ready capacity", snapshot.State)
	}
	if !snapshot.CountdownEndsAt.Equal(start.Add(6 * time.Second)) {
		t.Fatalf("countdown deadline = %s, want %s", snapshot.CountdownEndsAt, start.Add(6*time.Second))
	}
}

func TestGameSessionSupportsImmediateStartRules(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	session := newGameSession(t, start, gameDomain.SessionRules{
		MinimumPlayers:      2,
		MaximumPlayers:      4,
		ReadyWindowDuration: 0,
		CountdownDuration:   0,
	})
	if err := session.JoinPlayer("player-a", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.JoinPlayer("player-b", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-a", true, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-b", true, start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.State() != gameDomain.SessionRunning {
		t.Fatalf("state = %s, want running for immediate-start rules", session.State())
	}
}

func TestGameSessionReadyCommandIsIdempotent(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	session := newGameSession(t, start, gameDomain.SessionRules{
		MinimumPlayers:      2,
		MaximumPlayers:      4,
		ReadyWindowDuration: 30 * time.Second,
		CountdownDuration:   3 * time.Second,
	})
	if err := session.JoinPlayer("player-a", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-a", true, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	version := session.Version()
	if err := session.SetReady("player-a", true, start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	player, _ := session.Player("player-a")
	if player.State != gameDomain.PlayerReady {
		t.Fatalf("player state = %s, want ready", player.State)
	}
	if session.Version() != version {
		t.Fatalf("duplicate ready command changed version from %d to %d", version, session.Version())
	}
}

func TestGameSessionCancelsCountdownWhenPlayersDropBelowMinimum(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	session := newGameSession(t, start, gameDomain.SessionRules{
		MinimumPlayers:      2,
		MaximumPlayers:      2,
		ReadyWindowDuration: 30 * time.Second,
		CountdownDuration:   3 * time.Second,
	})
	if err := session.JoinPlayer("player-a", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.JoinPlayer("player-b", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-a", true, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-b", true, start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.LeavePlayer("player-b", start.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}

	snapshot := session.Snapshot()
	if snapshot.State != gameDomain.SessionCancelled || snapshot.EndReason != gameDomain.EndReasonInsufficientPlayers {
		t.Fatalf("cancelled snapshot = %+v", snapshot)
	}
	if err := session.JoinPlayer("player-c", start.Add(5*time.Second)); !errors.Is(err, gameDomain.ErrJoinClosed) {
		t.Fatalf("join terminal session error = %v, want ErrJoinClosed", err)
	}
}

func TestGameSessionSnapshotRestoreAndFinish(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	session := newGameSession(t, start, gameDomain.SessionRules{
		MinimumPlayers:      2,
		MaximumPlayers:      2,
		ReadyWindowDuration: 30 * time.Second,
		CountdownDuration:   0,
	})
	if err := session.JoinPlayer("player-a", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.JoinPlayer("player-b", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-a", true, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SetReady("player-b", true, start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.State() != gameDomain.SessionRunning {
		t.Fatalf("state = %s, want running with zero countdown", session.State())
	}

	restored, err := gameDomain.RestoreGameSession(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.PendingEvents()) != 0 {
		t.Fatal("restoring a snapshot unexpectedly produced domain events")
	}
	if err := restored.Finish("allPlayersCompleted", start.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshot := restored.Snapshot()
	if snapshot.State != gameDomain.SessionFinished || snapshot.EndReason != "allPlayersCompleted" {
		t.Fatalf("finished snapshot = %+v", snapshot)
	}
	for _, player := range snapshot.Players {
		if player.State != gameDomain.PlayerFinished {
			t.Fatalf("player %s state = %s, want finished", player.PlayerID, player.State)
		}
	}
}

func TestGameSessionRejectsInvalidRulesAndDefensiveCopiesPlayers(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	_, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{
		SessionID:       "session-invalid",
		HouseID:         "house-one",
		GameKey:         "test-game",
		ProtocolVersion: 1,
		Mode:            gameDomain.RealtimeGame,
		CreatedBy:       "player-a",
		CreatedAt:       start,
		Rules: gameDomain.SessionRules{
			MinimumPlayers:      1,
			MaximumPlayers:      4,
			ReadyWindowDuration: 30 * time.Second,
			CountdownDuration:   3 * time.Second,
		},
	})
	if !errors.Is(err, gameDomain.ErrMinimumPlayers) {
		t.Fatalf("invalid rules error = %v, want ErrMinimumPlayers", err)
	}

	session := newGameSession(t, start, gameDomain.SessionRules{
		MinimumPlayers:      2,
		MaximumPlayers:      4,
		ReadyWindowDuration: 30 * time.Second,
		CountdownDuration:   3 * time.Second,
	})
	if err := session.JoinPlayer("player-a", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	players := session.Players()
	players[0].State = gameDomain.PlayerLeft
	storedPlayer, _ := session.Player("player-a")
	if storedPlayer.State != gameDomain.PlayerWaiting {
		t.Fatalf("external player slice mutated aggregate state: %s", storedPlayer.State)
	}
	if err := session.SetReady("player-a", true, start); !errors.Is(err, gameDomain.ErrTimestampOutOfOrder) {
		t.Fatalf("out-of-order timestamp error = %v, want ErrTimestampOutOfOrder", err)
	}

	events := session.PendingEvents()
	if len(events) == 0 {
		t.Fatal("expected pending domain events")
	}
	events[0].Reason = "mutated"
	if session.PendingEvents()[0].Reason == "mutated" {
		t.Fatal("external event slice mutated aggregate events")
	}
	session.ClearPendingEvents()
	if len(session.PendingEvents()) != 0 {
		t.Fatal("pending events were not cleared")
	}
}

func newGameSession(t *testing.T, createdAt time.Time, rules gameDomain.SessionRules) *gameDomain.GameSession {
	t.Helper()
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{
		SessionID:       "session-one",
		HouseID:         "house-one",
		GameKey:         "test-game",
		ProtocolVersion: 1,
		Mode:            gameDomain.RealtimeGame,
		Rules:           rules,
		CreatedBy:       "player-a",
		CreatedAt:       createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func assertStrictEventVersions(t *testing.T, events []gameDomain.SessionEvent) {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no domain events were produced")
	}
	for index := 1; index < len(events); index++ {
		if events[index].Version != events[index-1].Version+1 {
			t.Fatalf("event versions are not consecutive: %+v", events)
		}
	}
}

func assertEventTypePresent(t *testing.T, events []gameDomain.SessionEvent, eventType gameDomain.SessionEventType) {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return
		}
	}
	t.Fatalf("event type %s was not produced: %+v", eventType, events)
}
