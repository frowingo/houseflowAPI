package gamecore

import (
	"bytes"
	"errors"
	"testing"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/tests/testkit/gamekit"
)

func TestGameMatchResultValidatesSharedMetadataOnly(t *testing.T) {
	now := time.Now().UTC()
	valid := gameDomain.MatchResult{SessionID: "session", HouseID: "house", GameKey: "anotherGame", SchemaVersion: 3, Status: gameDomain.MatchCompleted, EndReason: "roundsCompleted", StartedAt: now, EndedAt: now.Add(time.Second), Payload: []byte(`{"winningTeam":"blue","rounds":4}`)}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*gameDomain.MatchResult){
		"session":      func(r *gameDomain.MatchResult) { r.SessionID = "" },
		"house":        func(r *gameDomain.MatchResult) { r.HouseID = " " },
		"game":         func(r *gameDomain.MatchResult) { r.GameKey = "" },
		"schema":       func(r *gameDomain.MatchResult) { r.SchemaVersion = 0 },
		"status":       func(r *gameDomain.MatchResult) { r.Status = "running" },
		"reason":       func(r *gameDomain.MatchResult) { r.EndReason = " " },
		"start":        func(r *gameDomain.MatchResult) { r.StartedAt = time.Time{} },
		"end":          func(r *gameDomain.MatchResult) { r.EndedAt = now.Add(-time.Second) },
		"emptyPayload": func(r *gameDomain.MatchResult) { r.Payload = nil },
		"invalidJSON":  func(r *gameDomain.MatchResult) { r.Payload = []byte(`{"value":NaN}`) },
		"null":         func(r *gameDomain.MatchResult) { r.Payload = []byte(`null`) },
		"array":        func(r *gameDomain.MatchResult) { r.Payload = []byte(`[]`) },
	} {
		t.Run(name, func(t *testing.T) {
			result := valid
			mutate(&result)
			if !errors.Is(result.Validate(), gameDomain.ErrInvalidMatchResult) {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
	if _, err := houseRockets.DecodeMatchResult(valid); err == nil {
		t.Fatal("House Rockets accepted another game's payload")
	}
}

func TestHouseRocketsResultPersistenceCodecPreservesWirePayload(t *testing.T) {
	runtime, now := gamekit.NewRocketsRuntime(t)
	runtime.Reconcile([]string{"second"}, []string{"first", "second"})
	if err := runtime.Advance(now); err != nil {
		t.Fatal(err)
	}
	proposal := <-runtime.Results()
	stored, err := houseRockets.EncodeMatchResult(proposal)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := houseRockets.DecodeMatchResult(stored)
	if err != nil || !bytes.Equal(gamekit.MustJSON(t, proposal), gamekit.MustJSON(t, decoded)) {
		t.Fatalf("wire result changed: %+v %v", decoded, err)
	}
	for name, mutate := range map[string]func(*gameDomain.MatchResult){
		"schema":   func(r *gameDomain.MatchResult) { r.SchemaVersion++ },
		"identity": func(r *gameDomain.MatchResult) { r.HouseID = "otherHouse" },
		"status":   func(r *gameDomain.MatchResult) { r.Status = gameDomain.MatchCancelled },
		"reason":   func(r *gameDomain.MatchResult) { r.EndReason = "anotherReason" },
		"time":     func(r *gameDomain.MatchResult) { r.EndedAt = r.EndedAt.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			result := stored
			mutate(&result)
			if _, err := houseRockets.DecodeMatchResult(result); err == nil {
				t.Fatal("inconsistent stored result accepted")
			}
		})
	}
}
