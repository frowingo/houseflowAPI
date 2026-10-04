package tests

import (
	"testing"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
)

func TestGameSessionExclusionPreventsInvalidDeadlineStart(t *testing.T) {
	start := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)
	for _, countdown := range []bool{false, true} {
		rules := gameDomain.SessionRules{MinimumPlayers: 2, MaximumPlayers: 4, ReadyWindowDuration: 30 * time.Second, CountdownDuration: 3 * time.Second}
		if countdown {
			rules.ReadyWindowDuration = 0
		}
		session := newGameSession(t, start, rules)
		for _, id := range []string{"first", "second"} {
			if err := session.JoinPlayer(id, start); err != nil {
				t.Fatal(err)
			}
			if err := session.SetReady(id, true, start); err != nil {
				t.Fatal(err)
			}
		}
		session.ClearPendingEvents()
		if err := session.ExcludePlayers([]string{"first", "second"}, "connectionExpired", start.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if countdown && session.State() != gameDomain.SessionCancelled {
			t.Fatal("invalid countdown did not cancel")
		}
		if !countdown && session.State() != gameDomain.SessionLobby {
			t.Fatal("invalid ready window did not return to lobby")
		}
		for _, event := range session.PendingEvents() {
			if event.Type == gameDomain.GameStarted || event.Type == gameDomain.CountdownStarted {
				t.Fatal("exclusion started game")
			}
		}
		if _, err := gameDomain.RestoreGameSession(session.Snapshot()); err != nil {
			t.Fatal(err)
		}
		version := session.Version()
		if err := session.ExcludePlayers([]string{"first", "unknown"}, "connectionExpired", start.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		if session.Version() != version {
			t.Fatal("repeat exclusion changed state")
		}
	}
}
