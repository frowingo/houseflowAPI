package gamecore

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	ticTacToe "houseflowApi/internal/application/game/games/ticTacToe"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/data/database"
	"houseflowApi/tests/testkit/gamekit"

	"go.mongodb.org/mongo-driver/bson"
)

// House stats count completed matches per game: a win, a timeout and a
// departure each award one winner, a cancelled match counts nothing, results
// of a game this deployment no longer runs are skipped and only current
// members are listed.
func TestHouseGameStatsCountCompletedWins(t *testing.T) {
	fixture := gamekit.NewTurnGameFixture(t)
	stats := gameQueries.NewGetHouseGameStatsHandler(fixture.Matches, gamekit.RocketsGames(), fixture.MembershipPolicy())
	read := func(userID string) ([]gameQueries.GameStats, error) {
		return stats.Handle(fixture.Ctx, gameQueries.GetHouseGameStatsQuery{HouseID: fixture.HouseID, UserID: userID})
	}

	if empty, err := read(fixture.OwnerID); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("stats before any match = %#v %v", empty, err)
	}

	// The owner plays X and wins with three in a row.
	session := fixture.Start(t)
	for _, move := range []struct {
		player string
		cell   int
	}{{fixture.OwnerID, 0}, {fixture.MemberID, 3}, {fixture.OwnerID, 1}, {fixture.MemberID, 4}, {fixture.OwnerID, 2}} {
		if _, err := fixture.Play(session.SessionID, fixture.CommandID("move"), move.player, move.cell); err != nil {
			t.Fatal(err)
		}
	}
	// The owner's turn runs out: the member wins.
	session = fixture.Start(t)
	if _, err := fixture.Advance.Handle(fixture.Ctx, gameCommands.AdvanceGameSessionCommand{CommandID: fixture.CommandID("expire"), SessionID: session.SessionID, AdvanceAt: session.TurnDeadline}); err != nil {
		t.Fatal(err)
	}
	// The owner leaves mid-match: the member wins.
	session = fixture.Start(t)
	if _, err := fixture.Play(session.SessionID, fixture.CommandID("move"), fixture.OwnerID, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Leave.Handle(fixture.Ctx, gameCommands.LeaveGameSessionCommand{CommandID: fixture.CommandID("leave"), SessionID: session.SessionID, UserID: fixture.OwnerID}); err != nil {
		t.Fatal(err)
	}
	// A cancelled match has no winner and is not counted as played.
	session = fixture.Start(t)
	if _, err := fixture.Cancel.Handle(fixture.Ctx, gameCommands.CancelGameSessionCommand{CommandID: fixture.CommandID("cancel"), SessionID: session.SessionID, UserID: fixture.OwnerID}); err != nil {
		t.Fatal(err)
	}
	insertRetiredGameResult(t, fixture)

	got, err := read(fixture.MemberID)
	if err != nil {
		t.Fatal(err)
	}
	want := []gameQueries.GameStats{{GameKey: ticTacToe.GameKey, Players: []gameQueries.PlayerGameStats{
		{UserID: fixture.MemberID, Wins: 2, MatchesPlayed: 3},
		{UserID: fixture.OwnerID, Wins: 1, MatchesPlayed: 3},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stats = %+v, want %+v", got, want)
	}

	if _, err := read(fixture.OutsiderID); err == nil {
		t.Fatal("a non-member read the house stats")
	}

	// A member who left the house is no longer listed; the matches they
	// played still count for the members who played them.
	if _, err := fixture.DB.Collection("House").UpdateOne(fixture.Ctx, bson.M{}, bson.M{"$set": bson.M{"memberIds": []string{fixture.OwnerID}}}); err != nil {
		t.Fatal(err)
	}
	got, err = read(fixture.OwnerID)
	want = []gameQueries.GameStats{{GameKey: ticTacToe.GameKey, Players: []gameQueries.PlayerGameStats{{UserID: fixture.OwnerID, Wins: 1, MatchesPlayed: 3}}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("stats after the member left = %+v %v", got, err)
	}
}

// insertRetiredGameResult stores a completed result, as Complete stores it, of
// a game key no module is registered for.
func insertRetiredGameResult(t *testing.T, fixture *gamekit.TurnGameFixture) {
	t.Helper()
	payload := []byte(`{"winnerId":"` + fixture.OwnerID + `"}`)
	digest := sha256.Sum256(payload)
	now := time.Now().UTC()
	_, err := fixture.DB.Collection(database.GameMatchResultCollectionName).InsertOne(fixture.Ctx, bson.M{
		"_id": "retired-session", "houseId": fixture.HouseID, "gameKey": "flappyBird", "schemaVersion": int32(1),
		"status": "completed", "endReason": "finished", "startedAt": now.Add(-time.Minute), "endedAt": now,
		"resultEventType": "flappyBird.result", "runtimeEpoch": int64(1), "payload": payload,
		"payloadHash": hex.EncodeToString(digest[:]), "createdAt": now,
	})
	if err != nil {
		t.Fatal(err)
	}
}
