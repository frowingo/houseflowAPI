package tests

import (
	"errors"
	"testing"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	"houseflowApi/internal/config"
)

func TestHouseRocketsActivationIsExplicitAndIndependentOfAppEnvironment(t *testing.T) {
	for _, environment := range []string{"", "local", "development", "staging", "production", "prod", "unknown"} {
		for _, flag := range []string{"", "false", "true"} {
			t.Setenv("APP_ENV", environment)
			t.Setenv("HOUSE_ROCKETS_ENABLED", flag)
			if config.HouseRocketsEnabled() != (flag == "true") {
				t.Fatalf("env=%q flag=%q", environment, flag)
			}
		}
	}
}

func TestHouseRocketsCatalogActivationPreservesDefaultV1(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		catalog, err := gameApplication.NewDefaultCatalog(gameApplication.CatalogOptions{EnableHouseRockets: enabled})
		if err != nil {
			t.Fatal(err)
		}
		flappy, err := catalog.Find(gameApplication.FlappyBirdGameKey)
		if err != nil || flappy.ProtocolVersion != 1 {
			t.Fatalf("flappy regression: %+v %v", flappy, err)
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
