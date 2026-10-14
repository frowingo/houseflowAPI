package tests

import (
	"errors"
	"houseflowApi/internal/application/game/games"
	"testing"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
)

// Without the realtime coordinator no tick game is registered, whatever its
// Game document says.
func TestHouseRocketsIsRegisteredOnlyWithTickGames(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		catalog, err := builtinCatalog(games.Options{Realtime: enabled})
		if err != nil {
			t.Fatal(err)
		}
		rockets, err := catalog.Find(houseRockets.GameKey)
		if enabled && (err != nil || rockets.ProtocolVersion != 2 || rockets.Rules.MaximumPlayers != 8) {
			t.Fatalf("rockets: %+v %v", rockets, err)
		}
		if !enabled && !errors.Is(err, gameAbstract.ErrGameDefinitionNotFound) {
			t.Fatalf("disabled catalog: %v", err)
		}
	}
}

func TestHouseRocketsWireSnapshotHidesOtherControllersAndProposedWinner(t *testing.T) {
	runtime, now := newRocketsRuntime(t)
	first := bindRocketsControl(t, runtime, "first", "first-connection", now)
	_ = bindRocketsControl(t, runtime, "second", "second-connection", now)
	frame := runtimeFrame(t, runtime)
	model := houseRockets.SnapshotForConnection(frame, "first", "first-connection", &first)
	if model.Players[0].ControlGeneration == nil || model.Players[1].ControlGeneration != nil {
		t.Fatalf("control privacy: %+v", model.Players)
	}
	spectator := houseRockets.SnapshotForConnection(frame, "first", "wrong-connection", &first)
	if spectator.Players[0].ControlGeneration != nil {
		t.Fatal("generation leaked to another connection")
	}
	runtime.Reconcile([]string{"second"}, []string{"first", "second"})
	if err := runtime.Advance(now); err != nil {
		t.Fatal(err)
	}
	frame = runtimeFrame(t, runtime)
	if frame.Phase != houseRockets.PhaseFinalizing {
		t.Fatalf("phase: %s", frame.Phase)
	}
	model = houseRockets.SnapshotForConnection(frame, "first", "first-connection", &first)
	if model.WinnerID != nil || model.Players[0].ControlGeneration != nil || model.Players[0].EliminationReason == nil || *model.Players[0].EliminationReason != houseRockets.EliminationForfeit {
		t.Fatalf("finalizing wire: %+v", model)
	}
}

func TestHouseRocketsRuntimeMixedReconciliationEliminatesAsOneBatch(t *testing.T) {
	runtime, now := newRocketsRuntime(t)
	runtime.Reconcile(nil, []string{"first"})
	if err := runtime.Advance(now); err != nil {
		t.Fatal(err)
	}
	frame := runtimeFrame(t, runtime)
	if frame.Phase != houseRockets.PhaseFinalizing || frame.World.Players[0].IsAlive || frame.World.Players[1].IsAlive {
		t.Fatalf("mixed batch: %+v", frame)
	}
	if *frame.World.Players[0].EliminationReason != houseRockets.EliminationForfeit || *frame.World.Players[1].EliminationReason != houseRockets.EliminationMembershipRevoked {
		t.Fatal("mixed reasons lost")
	}
}
