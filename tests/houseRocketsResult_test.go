package tests

import (
	"math"
	"testing"
	"time"

	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
)

func TestHouseRocketsRuntimePublishesOneProposalAndWinnerOnlyAfterCommit(t *testing.T) {
	runtime, now := newRocketsRuntime(t)
	runtime.Reconcile([]string{"second"}, []string{"first", "second"})
	if err := runtime.Advance(now); err != nil {
		t.Fatal(err)
	}
	finalizing := runtimeFrame(t, runtime)
	if model := houseRockets.SnapshotForConnection(finalizing, "second", "conn", nil); model.WinnerID != nil || model.Phase != houseRockets.PhaseFinalizing {
		t.Fatalf("uncommitted winner: %+v", model)
	}
	proposal := <-runtime.Results()
	if proposal.WinnerID == nil || *proposal.WinnerID != "second" {
		t.Fatalf("proposal: %+v", proposal)
	}
	if err := runtime.Advance(now); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.Results():
		t.Fatal("duplicate proposal")
	default:
	}
	frame := runtime.CommittedFrame(proposal)
	model := houseRockets.SnapshotForConnection(frame, "second", "conn", nil)
	if model.Phase != houseRockets.PhaseEnded || model.WinnerID == nil || *model.WinnerID != "second" || frame.StateSequence <= finalizing.StateSequence {
		t.Fatalf("committed frame: %+v", model)
	}
}

func TestHouseRocketsResultValidationRejectsInconsistentResults(t *testing.T) {
	runtime, now := newRocketsRuntime(t)
	runtime.Reconcile([]string{"second"}, []string{"first", "second"})
	if err := runtime.Advance(now); err != nil {
		t.Fatal(err)
	}
	proposal := <-runtime.Results()
	snapshot := gameDomain.SessionSnapshot{SessionID: proposal.SessionID, HouseID: proposal.HouseID, GameKey: houseRockets.GameKey, State: gameDomain.SessionRunning, StartedAt: now, Rules: houseRockets.Definition().Rules, Players: []gameDomain.SessionPlayer{{PlayerID: "first", ReadyAt: now, State: gameDomain.PlayerPlaying}, {PlayerID: "second", ReadyAt: now, State: gameDomain.PlayerPlaying}}}
	if err := houseRockets.ValidateResult(proposal, snapshot); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*houseRockets.HouseRocketsResultModel){
		"nilStart": func(result *houseRockets.HouseRocketsResultModel) { result.StartedAt = nil },
		"wrongStart": func(result *houseRockets.HouseRocketsResultModel) {
			value := now.Add(time.Second)
			result.StartedAt = &value
		},
		"protocol":           func(result *houseRockets.HouseRocketsResultModel) { result.ProtocolVersion = 1 },
		"house":              func(result *houseRockets.HouseRocketsResultModel) { result.HouseID = "other-house" },
		"winner":             func(result *houseRockets.HouseRocketsResultModel) { value := "first"; result.WinnerID = &value },
		"rank":               func(result *houseRockets.HouseRocketsResultModel) { value := 8; result.Players[1].Rank = &value },
		"duplicates":         func(result *houseRockets.HouseRocketsResultModel) { result.Players[1].PlayerID = "first" },
		"outsider":           func(result *houseRockets.HouseRocketsResultModel) { result.Players[0].PlayerID = "other-user" },
		"negativeDistance":   func(result *houseRockets.HouseRocketsResultModel) { result.Players[0].Distance = -1 },
		"impossibleDistance": func(result *houseRockets.HouseRocketsResultModel) { result.Players[0].Distance = 10000 },
		"nan":                func(result *houseRockets.HouseRocketsResultModel) { result.Players[0].Distance = math.NaN() },
		"partialTick":        func(result *houseRockets.HouseRocketsResultModel) { result.DurationSeconds = 0.00001 },
		"eliminationAfterEnd": func(result *houseRockets.HouseRocketsResultModel) {
			value := int64(1)
			result.Players[0].EliminatedAtTick = &value
		},
		"earlyEnd": func(result *houseRockets.HouseRocketsResultModel) { result.EndedAt = now.Add(-time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			result := proposal
			result.Players = append([]houseRockets.HouseRocketsPlayerResultModel(nil), proposal.Players...)
			mutate(&result)
			if err := houseRockets.ValidateResult(result, snapshot); err == nil {
				t.Fatal("inconsistent result accepted")
			}
		})
	}
}
