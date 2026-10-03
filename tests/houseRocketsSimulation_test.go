package tests

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"houseflowApi/internal/application/game/gameSpesific/houseRockets"
)

type houseRocketsExpectedBody struct {
	PlayerIndex            int                       `json:"playerIndex"`
	WorldX                 float64                   `json:"worldX"`
	WorldY                 float64                   `json:"worldY"`
	CourseHeading          float64                   `json:"courseHeading"`
	IsAlive                bool                      `json:"isAlive"`
	SpeedEffect            *houseRockets.SpeedEffect `json:"speedEffect"`
	EffectRemainingSeconds float64                   `json:"effectRemainingSeconds"`
	TouchedFieldIDs        []string                  `json:"touchedFieldIds"`
}

type houseRocketsSimulationFixture struct {
	AbsoluteTolerance float64 `json:"absoluteTolerance"`
	Spawns            []struct {
		PlayerCount int                        `json:"playerCount"`
		Players     []houseRocketsExpectedBody `json:"players"`
	} `json:"spawns"`
	Gates []struct {
		Index int `json:"index"`
		houseRockets.Gate
	} `json:"gates"`
	SpeedFields []struct {
		Index   int  `json:"index"`
		Spawned bool `json:"spawned"`
		houseRockets.SpeedField
		Positions []struct {
			ElapsedSeconds float64 `json:"elapsedSeconds"`
			WorldY         float64 `json:"worldY"`
		} `json:"positions"`
	} `json:"speedFields"`
	CourseAngles []struct {
		ElapsedSeconds   float64 `json:"elapsedSeconds"`
		CourseAngle      float64 `json:"courseAngle"`
		IsTransitioning  bool    `json:"isTransitioning"`
		TargetIsVertical *bool   `json:"targetIsVertical"`
	} `json:"courseAngles"`
	Contacts []struct {
		Name     string               `json:"name"`
		Point    houseRockets.Point   `json:"point"`
		Radius   float64              `json:"radius"`
		Polygon  houseRockets.Polygon `json:"polygon"`
		Expected houseRockets.Point   `json:"expected"`
	} `json:"contacts"`
	BehindCamera []struct {
		CenterX  float64 `json:"centerX"`
		CameraX  float64 `json:"cameraX"`
		Expected bool    `json:"expected"`
	} `json:"behindCamera"`
	Motions []struct {
		Name           string    `json:"name"`
		PlayerCount    int       `json:"playerCount"`
		CourseHeadings []float64 `json:"courseHeadings"`
		RequestedTicks int       `json:"requestedTicks"`
		Expected       struct {
			Tick           int64                      `json:"tick"`
			ElapsedSeconds float64                    `json:"elapsedSeconds"`
			CameraX        float64                    `json:"cameraX"`
			CourseAngle    float64                    `json:"courseAngle"`
			Players        []houseRocketsExpectedBody `json:"players"`
		} `json:"expected"`
	} `json:"motions"`
}

func newHouseRocketsParams(count int) houseRockets.NewSimulationParams {
	params := houseRockets.NewSimulationParams{SessionID: "session", HouseID: "house", StartedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	for index := range count {
		params.Players = append(params.Players, houseRockets.PlayerIdentity{PlayerID: fmt.Sprintf("p%d", index+1), DisplayName: fmt.Sprintf("Player %d", index+1)})
	}
	return params
}

func newHouseRocketsSimulation(t *testing.T, count int) *houseRockets.Simulation {
	t.Helper()
	simulation, err := houseRockets.NewSimulation(newHouseRocketsParams(count))
	if err != nil {
		t.Fatal(err)
	}
	return simulation
}

func advanceHouseRockets(t *testing.T, simulation *houseRockets.Simulation, ticks int) {
	t.Helper()
	for ticks > 0 && simulation.State().Outcome == nil {
		batch := min(ticks, houseRockets.MaximumAdvanceTicks)
		if err := simulation.AdvanceTicks(batch); err != nil {
			t.Fatal(err)
		}
		ticks -= batch
	}
}

func assertHouseRocketsFloat(t *testing.T, name string, expected, actual, tolerance float64) {
	t.Helper()
	if math.IsNaN(actual) || math.IsInf(actual, 0) || math.Abs(expected-actual) > tolerance {
		t.Fatalf("%s: expected %.16g, got %.16g (tolerance %g)", name, expected, actual, tolerance)
	}
}

func assertHouseRocketsBodies(t *testing.T, expected []houseRocketsExpectedBody, actual []houseRockets.PlayerState, tolerance float64) {
	t.Helper()
	if len(expected) != len(actual) {
		t.Fatal("player count differs")
	}
	for _, body := range expected {
		player := actual[body.PlayerIndex]
		assertHouseRocketsFloat(t, "worldX", body.WorldX, player.WorldX, tolerance)
		assertHouseRocketsFloat(t, "worldY", body.WorldY, player.WorldY, tolerance)
		assertHouseRocketsFloat(t, "courseHeading", body.CourseHeading, player.CourseHeading, tolerance)
		assertHouseRocketsFloat(t, "effectRemainingSeconds", body.EffectRemainingSeconds, player.EffectRemainingSeconds, tolerance)
		if body.IsAlive != player.IsAlive || !reflect.DeepEqual(body.SpeedEffect, player.SpeedEffect) {
			t.Fatal("alive/effect state differs from Swift reference")
		}
		if body.TouchedFieldIDs != nil && !reflect.DeepEqual(body.TouchedFieldIDs, player.TouchedFieldIDs) {
			t.Fatal("contact history differs from Swift reference")
		}
	}
}

func TestHouseRocketsSimulationGoldenFixtures(t *testing.T) {
	fixture := readHouseRocketsFixture[houseRocketsSimulationFixture](t, "houseRocketsSimulation")
	for _, entry := range fixture.Spawns {
		t.Run(fmt.Sprintf("spawn%d", entry.PlayerCount), func(t *testing.T) {
			snapshot := newHouseRocketsSimulation(t, entry.PlayerCount).Snapshot()
			assertHouseRocketsBodies(t, entry.Players, snapshot.Players, fixture.AbsoluteTolerance)
			for index, player := range snapshot.Players {
				if player.PlayerID != fmt.Sprintf("p%d", index+1) || player.Color != houseRockets.PlayerColors()[index] {
					t.Fatal("roster order/colors changed")
				}
			}
		})
	}
	for _, entry := range fixture.Motions {
		t.Run(entry.Name, func(t *testing.T) {
			simulation := newHouseRocketsSimulation(t, entry.PlayerCount)
			for index, heading := range entry.CourseHeadings {
				if err := simulation.Steer(fmt.Sprintf("p%d", index+1), heading); err != nil {
					t.Fatal(err)
				}
			}
			advanceHouseRockets(t, simulation, entry.RequestedTicks)
			snapshot := simulation.Snapshot()
			if snapshot.Tick != entry.Expected.Tick {
				t.Fatalf("tick: expected %d, got %d", entry.Expected.Tick, snapshot.Tick)
			}
			assertHouseRocketsFloat(t, "elapsedSeconds", entry.Expected.ElapsedSeconds, snapshot.ElapsedSeconds, fixture.AbsoluteTolerance)
			assertHouseRocketsFloat(t, "cameraX", entry.Expected.CameraX, snapshot.CameraX, fixture.AbsoluteTolerance)
			assertHouseRocketsFloat(t, "courseAngle", entry.Expected.CourseAngle, snapshot.CourseAngle, fixture.AbsoluteTolerance)
			assertHouseRocketsBodies(t, entry.Expected.Players, snapshot.Players, fixture.AbsoluteTolerance)
			if _, err := houseRockets.RestoreSimulation(simulation.State()); err != nil {
				t.Fatalf("generated state cannot restore: %v", err)
			}
			if entry.Name == "behindCameraElimination" {
				if snapshot.Outcome == nil || snapshot.Outcome.WinnerID == nil || *snapshot.Outcome.WinnerID != "p2" || *snapshot.Players[0].EliminatedAtTick != 173 {
					t.Fatal("winner/elimination tick differs")
				}
			}
		})
	}
}

func TestHouseRocketsCourseGoldenFixtures(t *testing.T) {
	fixture := readHouseRocketsFixture[houseRocketsSimulationFixture](t, "houseRocketsSimulation")
	for _, entry := range fixture.Gates {
		gate, err := houseRockets.GateAt(entry.Index)
		if err != nil || !reflect.DeepEqual(gate, entry.Gate) {
			t.Fatalf("gate %d differs: %+v (%v)", entry.Index, gate, err)
		}
		polygons := gate.SolidPolygons()
		if len(polygons) != 2*(len(gate.Sections)-1) {
			t.Fatal("missing actual course faces")
		}
		for _, polygon := range polygons {
			if _, err := houseRockets.ResolveContact(houseRockets.Point{X: gate.WorldX - 300, Y: 180}, 10, polygon); err != nil {
				t.Fatalf("invalid generated polygon: %v", err)
			}
		}
	}
	for _, entry := range fixture.SpeedFields {
		field, err := houseRockets.SpeedFieldAt(entry.Index)
		if err != nil || field.ID != entry.ID || field.Effect != entry.Effect {
			t.Fatalf("field %d differs", entry.Index)
		}
		assertHouseRocketsFloat(t, "field worldX", entry.WorldX, field.WorldX, fixture.AbsoluteTolerance)
		assertHouseRocketsFloat(t, "field phase", entry.Phase, field.Phase, fixture.AbsoluteTolerance)
		assertHouseRocketsFloat(t, "field period", entry.PeriodSeconds, field.PeriodSeconds, fixture.AbsoluteTolerance)
		if entry.Spawned != (entry.Index%5 != 4) {
			t.Fatal("field skip rule differs")
		}
		for _, position := range entry.Positions {
			assertHouseRocketsFloat(t, "field worldY", position.WorldY, field.WorldYAt(position.ElapsedSeconds), fixture.AbsoluteTolerance)
		}
	}
	for _, entry := range fixture.CourseAngles {
		orientation, err := houseRockets.CourseOrientationAt(entry.ElapsedSeconds)
		if err != nil {
			t.Fatal(err)
		}
		assertHouseRocketsFloat(t, "courseAngle", entry.CourseAngle, orientation.Angle, fixture.AbsoluteTolerance)
		if orientation.IsTransitioning != entry.IsTransitioning || !reflect.DeepEqual(orientation.TargetIsVertical, entry.TargetIsVertical) {
			t.Fatalf("turn warning/state at %g differs", entry.ElapsedSeconds)
		}
	}
	for _, entry := range fixture.Contacts {
		t.Run(entry.Name, func(t *testing.T) {
			point, err := houseRockets.ResolveContact(entry.Point, entry.Radius, entry.Polygon)
			if err != nil {
				t.Fatal(err)
			}
			assertHouseRocketsFloat(t, "contact X", entry.Expected.X, point.X, fixture.AbsoluteTolerance)
			assertHouseRocketsFloat(t, "contact Y", entry.Expected.Y, point.Y, fixture.AbsoluteTolerance)
		})
	}
	for _, entry := range fixture.BehindCamera {
		if houseRockets.IsBehindCamera(entry.CenterX, entry.CameraX) != entry.Expected {
			t.Fatal("rear edge must use strict full-circle exit")
		}
	}
}

func TestHouseRocketsSimulationInvalidInputIsAtomic(t *testing.T) {
	params := newHouseRocketsParams(2)
	invalidParams := []houseRockets.NewSimulationParams{newHouseRocketsParams(1), newHouseRocketsParams(9)}
	for _, change := range []func(*houseRockets.NewSimulationParams){
		func(p *houseRockets.NewSimulationParams) { p.SessionID = "" }, func(p *houseRockets.NewSimulationParams) { p.HouseID = "" },
		func(p *houseRockets.NewSimulationParams) { p.StartedAt = time.Time{} }, func(p *houseRockets.NewSimulationParams) { p.Players[0].PlayerID = " p1" },
		func(p *houseRockets.NewSimulationParams) { p.Players[0].DisplayName = " " }, func(p *houseRockets.NewSimulationParams) { p.Players[1].PlayerID = p.Players[0].PlayerID },
	} {
		p := newHouseRocketsParams(2)
		change(&p)
		invalidParams = append(invalidParams, p)
	}
	for _, p := range invalidParams {
		if _, err := houseRockets.NewSimulation(p); !errors.Is(err, houseRockets.ErrInvalidSimulation) {
			t.Fatal("invalid constructor accepted")
		}
	}
	simulation, err := houseRockets.NewSimulation(params)
	if err != nil {
		t.Fatal(err)
	}
	initial := simulation.State()
	for _, heading := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if !errors.Is(simulation.Steer("p1", heading), houseRockets.ErrInvalidInput) {
			t.Fatal("nonfinite heading accepted")
		}
	}
	if !errors.Is(simulation.Steer("missing", 0), houseRockets.ErrPlayerNotFound) {
		t.Fatal("unknown actor accepted")
	}
	for _, ticks := range []int{-1, 31, math.MaxInt} {
		if !errors.Is(simulation.AdvanceTicks(ticks), houseRockets.ErrInvalidAdvance) {
			t.Fatal("unbounded delta accepted")
		}
	}
	if err := simulation.AdvanceTicks(0); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{{}, {"p1", "missing"}, {"p1", "p1"}} {
		if simulation.EliminatePlayers(ids, houseRockets.EliminationForfeit) == nil {
			t.Fatal("invalid elimination batch accepted")
		}
	}
	if simulation.EliminatePlayers([]string{"p1"}, houseRockets.EliminationBehindCamera) == nil {
		t.Fatal("system operation must not manufacture physics elimination")
	}
	if simulation.Cancel(houseRockets.EndLastSurvivor) == nil || simulation.Cancel(houseRockets.EndSessionExpired) == nil {
		t.Fatal("invalid/premature cancellation accepted")
	}
	if !reflect.DeepEqual(initial, simulation.State()) {
		t.Fatal("invalid command partially mutated state")
	}
	if err := simulation.Steer("p1", 5*math.Pi/2); err != nil {
		t.Fatal(err)
	}
	assertHouseRocketsFloat(t, "normalized heading", math.Pi/2, simulation.State().Players[0].CourseHeading, 1e-12)
	for _, elapsed := range []float64{-1, math.NaN(), math.Inf(1)} {
		if _, err := houseRockets.CourseOrientationAt(elapsed); err == nil {
			t.Fatal("invalid course time accepted")
		}
	}
	if _, err := houseRockets.GateAt(-1); err == nil {
		t.Fatal("negative gate index accepted")
	}
	if _, err := houseRockets.SpeedFieldAt(math.MaxInt); err == nil {
		t.Fatal("unbounded field index accepted")
	}
	if len((houseRockets.Gate{}).SolidPolygons()) != 0 {
		t.Fatal("empty geometry is not safe")
	}
	polygon := houseRockets.Polygon{{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 100}, {X: 0, Y: 100}}
	for _, radius := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if _, err := houseRockets.ResolveContact(houseRockets.Point{X: 10, Y: 10}, radius, polygon); err == nil {
			t.Fatal("invalid radius accepted")
		}
	}
	if _, err := houseRockets.ResolveContact(houseRockets.Point{X: math.NaN()}, 10, polygon); err == nil {
		t.Fatal("invalid contact point accepted")
	}
	for _, bad := range []houseRockets.Polygon{nil, {{X: 0, Y: 0}, {X: 0, Y: 100}, {X: 100, Y: 100}, {X: 100, Y: 0}}, {{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 2, Y: 0}}} {
		if _, err := houseRockets.ResolveContact(houseRockets.Point{}, 10, bad); err == nil {
			t.Fatal("invalid polygon accepted")
		}
	}
}

func TestHouseRocketsFixedStepsAreFrameRateIndependent(t *testing.T) {
	var baseline houseRockets.SimulationState
	for _, fps := range []int{30, 60, 120, 144} {
		simulation := newHouseRocketsSimulation(t, 8)
		for index := range 8 {
			if err := simulation.Steer(fmt.Sprintf("p%d", index+1), -math.Pi/16); err != nil {
				t.Fatal(err)
			}
		}
		for frame := 1; frame <= 3*fps; frame++ {
			ticks := frame*houseRockets.PhysicsRateHz/fps - (frame-1)*houseRockets.PhysicsRateHz/fps
			if err := simulation.AdvanceTicks(ticks); err != nil {
				t.Fatal(err)
			}
		}
		if fps == 30 {
			baseline = simulation.State()
		} else if !reflect.DeepEqual(baseline, simulation.State()) {
			t.Fatalf("%d FPS produces different world state", fps)
		}
	}
	if baseline.Tick != 360 {
		t.Fatal("tick drift")
	}
}

func restoreHouseRocketsTestState(t *testing.T, state houseRockets.SimulationState) *houseRockets.Simulation {
	t.Helper()
	simulation, err := houseRockets.RestoreSimulation(state)
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	return simulation
}

func houseRocketsFieldState(t *testing.T, fieldIndex int, effect *houseRockets.SpeedEffect) houseRockets.SimulationState {
	t.Helper()
	state := newHouseRocketsSimulation(t, 2).State()
	state.Tick = 1200
	field, err := houseRockets.SpeedFieldAt(fieldIndex)
	if err != nil {
		t.Fatal(err)
	}
	speed := houseRockets.BaseSpeed
	if effect != nil && *effect == houseRockets.EffectBoost {
		speed *= houseRockets.BoostMultiplier
	}
	state.Players[0].WorldX = field.WorldX - speed/120
	state.Players[0].WorldY = field.WorldYAt(float64(state.Tick+1) / 120)
	state.Players[0].SpeedEffect = effect
	if effect != nil {
		state.Players[0].EffectRemainingSeconds = 1
	}
	state.Players[1].WorldX, state.Players[1].WorldY = state.Players[0].WorldX, 10
	state.CameraX = state.Players[0].WorldX - houseRockets.LeaderAnchorX
	state.NextGateIndex = int(math.Ceil((state.CameraX + houseRockets.ViewportWidth + 620 - houseRockets.FirstGateX) / houseRockets.GateSpacing))
	return state
}

func TestHouseRocketsFieldsApplyOnceReplaceAndExpire(t *testing.T) {
	for _, fieldIndex := range []int{0, 1} {
		t.Run(fmt.Sprintf("field%d", fieldIndex), func(t *testing.T) {
			var initialEffect *houseRockets.SpeedEffect
			if fieldIndex == 1 {
				effect := houseRockets.EffectBoost
				initialEffect = &effect
			}
			simulation := restoreHouseRocketsTestState(t, houseRocketsFieldState(t, fieldIndex, initialEffect))
			advanceHouseRockets(t, simulation, 1)
			player := simulation.State().Players[0]
			effect, multiplier, duration := houseRockets.EffectBoost, houseRockets.BoostMultiplier, houseRockets.BoostDurationSeconds
			if fieldIndex == 1 {
				effect, multiplier, duration = houseRockets.EffectSlow, houseRockets.SlowMultiplier, houseRockets.SlowDurationSeconds
			}
			if player.SpeedEffect == nil || *player.SpeedEffect != effect || player.EffectRemainingSeconds != duration || player.CourseHeading != 0 || !reflect.DeepEqual(player.TouchedFieldIDs, []string{fmt.Sprintf("field:%d", fieldIndex)}) {
				t.Fatal("field contact did not apply exact effect")
			}
			beforeX := player.WorldX
			advanceHouseRockets(t, simulation, 1)
			player = simulation.State().Players[0]
			assertHouseRocketsFloat(t, "effect speed", houseRockets.BaseSpeed*multiplier/120, player.WorldX-beforeX, 1e-9)
			assertHouseRocketsFloat(t, "effect not reapplied", duration-1.0/120, player.EffectRemainingSeconds, 1e-12)
			for _, id := range []string{"p1", "p2"} {
				if err := simulation.Steer(id, math.Pi/2); err != nil {
					t.Fatal(err)
				}
			}
			advanceHouseRockets(t, simulation, 180)
			player = simulation.State().Players[0]
			if !player.IsAlive || player.SpeedEffect != nil || player.EffectRemainingSeconds != 0 || player.CourseHeading != math.Pi/2 {
				t.Fatal("effect did not expire independently of heading")
			}
			if err := simulation.Steer("p1", 0); err != nil {
				t.Fatal(err)
			}
			beforeX = player.WorldX
			advanceHouseRockets(t, simulation, 1)
			assertHouseRocketsFloat(t, "restored base speed", 2.5, simulation.State().Players[0].WorldX-beforeX, 1e-9)
		})
	}
	state := houseRocketsFieldState(t, 0, nil)
	state.Players[1].WorldY = state.Players[0].WorldY
	simulation := restoreHouseRocketsTestState(t, state)
	advanceHouseRockets(t, simulation, 1)
	players := simulation.State().Players
	if players[0].WorldX != players[1].WorldX || players[0].WorldY != players[1].WorldY || players[0].SpeedEffect == nil || players[1].SpeedEffect == nil {
		t.Fatal("players must pass through each other and touch fields independently")
	}
}

func TestHouseRocketsOutcomeRanksAndSystemElimination(t *testing.T) {
	simulation := newHouseRocketsSimulation(t, 4)
	advanceHouseRockets(t, simulation, 1)
	if err := simulation.EliminatePlayers([]string{"p4"}, houseRockets.EliminationConnectionExpired); err != nil {
		t.Fatal(err)
	}
	if err := simulation.EliminatePlayers([]string{"p4"}, houseRockets.EliminationConnectionExpired); err != nil {
		t.Fatal("duplicate elimination is not idempotent")
	}
	if err := simulation.Steer("p3", math.Pi/4); err != nil {
		t.Fatal(err)
	}
	advanceHouseRockets(t, simulation, 2)
	if err := simulation.EliminatePlayers([]string{"p2", "p3"}, houseRockets.EliminationForfeit); err != nil {
		t.Fatal(err)
	}
	state := simulation.State()
	if state.Outcome == nil || state.Outcome.WinnerID == nil || *state.Outcome.WinnerID != "p1" {
		t.Fatal("wrong survivor")
	}
	result, err := simulation.ProposeResult(newHouseRocketsParams(4).StartedAt.Add(10 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for index, rank := range []int{1, 2, 2, 4} {
		if result.Players[index].Rank == nil || *result.Players[index].Rank != rank {
			t.Fatal("competition ranking/tie broken by distance")
		}
	}
	if result.DurationSeconds != 3.0/120 || result.Players[1].Distance == result.Players[2].Distance || result.Players[3].EliminationReason == nil || *result.Players[3].EliminationReason != houseRockets.EliminationConnectionExpired {
		t.Fatal("result duration/reason/distance differs")
	}
	if !errors.Is(simulation.AdvanceTicks(1), houseRockets.ErrSimulationFinished) || !errors.Is(simulation.Steer("p1", 0), houseRockets.ErrSimulationFinished) || !errors.Is(simulation.Steer("p2", 0), houseRockets.ErrPlayerEliminated) {
		t.Fatal("terminal/eliminated player advanced")
	}
	if !errors.Is(simulation.Cancel(houseRockets.EndRecoveryFailed), houseRockets.ErrSimulationFinished) {
		t.Fatal("committed domain outcome overwritten")
	}
	if !reflect.DeepEqual(state, simulation.State()) {
		t.Fatal("terminal result changed")
	}
	if _, err := simulation.ProposeResult(time.Time{}); err == nil {
		t.Fatal("invalid finish time accepted")
	}
	if _, err := simulation.ProposeResult(newHouseRocketsParams(2).StartedAt.Add(-time.Second)); err == nil {
		t.Fatal("out-of-order finish time accepted")
	}
	if _, err := newHouseRocketsSimulation(t, 2).ProposeResult(time.Now()); !errors.Is(err, houseRockets.ErrSimulationNotFinished) {
		t.Fatal("ongoing match has result")
	}
	draw := newHouseRocketsSimulation(t, 2)
	if err := draw.EliminatePlayers([]string{"p1", "p2"}, houseRockets.EliminationMembershipRevoked); err != nil {
		t.Fatal(err)
	}
	drawResult, err := draw.ProposeResult(newHouseRocketsParams(2).StartedAt)
	if err != nil || drawResult.WinnerID != nil || drawResult.EndReason != houseRockets.EndSimultaneousElimination {
		t.Fatal("batch must resolve as a draw")
	}
	for _, player := range drawResult.Players {
		if *player.Rank != 1 || *player.EliminatedAtTick != 0 {
			t.Fatal("zero-tick tie lost")
		}
	}
}

func TestHouseRocketsSimultaneousPhysicalExitAndDeadlinePriority(t *testing.T) {
	for _, tick := range []int64{120, houseRockets.MaximumMatchTicks - 1} {
		for _, draw := range []bool{true, false} {
			state := newHouseRocketsSimulation(t, 2).State()
			state.Tick, state.CameraX = tick, 100
			for index := range state.Players {
				state.Players[index].WorldX, state.Players[index].CourseHeading = 90, math.Pi
			}
			if !draw {
				state.Players[1].WorldX, state.Players[1].CourseHeading = 500, 0
			}
			simulation := restoreHouseRocketsTestState(t, state)
			advanceHouseRockets(t, simulation, 1)
			snapshot := simulation.Snapshot()
			if snapshot.Tick != tick+1 || snapshot.Outcome == nil || snapshot.Outcome.Status != houseRockets.ResultCompleted {
				t.Fatal("normal resolution must take precedence over deadline")
			}
			if draw {
				if snapshot.Outcome.WinnerID != nil || snapshot.Outcome.EndReason != houseRockets.EndSimultaneousElimination || snapshot.Players[0].IsAlive || snapshot.Players[1].IsAlive || *snapshot.Players[0].EliminatedAtTick != *snapshot.Players[1].EliminatedAtTick {
					t.Fatal("physical exits resolved before entire tick")
				}
			} else if snapshot.Outcome.WinnerID == nil || *snapshot.Outcome.WinnerID != "p2" {
				t.Fatal("last survivor lost at the deadline")
			}
		}
	}
}

func TestHouseRocketsFiveMinuteExpiryAndTurnsDoNotMoveCamera(t *testing.T) {
	simulation := newHouseRocketsSimulation(t, 2)
	for _, id := range []string{"p1", "p2"} {
		if err := simulation.Steer(id, math.Pi/2); err != nil {
			t.Fatal(err)
		}
	}
	advanceHouseRockets(t, simulation, int(houseRockets.MaximumMatchTicks))
	snapshot := simulation.Snapshot()
	if snapshot.Tick != houseRockets.MaximumMatchTicks || snapshot.ElapsedSeconds != 300 || snapshot.Outcome == nil || snapshot.Outcome.Status != houseRockets.ResultCancelled || snapshot.Outcome.EndReason != houseRockets.EndSessionExpired || snapshot.Outcome.WinnerID != nil || snapshot.CameraX != 0 {
		t.Fatal("stalled match must expire without manufactured winner/camera movement")
	}
	for _, player := range snapshot.Players {
		if !player.IsAlive || player.WorldX != 250 || player.WorldY != 350 || player.CourseHeading != math.Pi/2 {
			t.Fatal("course turn changed world physics")
		}
	}
	result, err := simulation.ProposeResult(newHouseRocketsParams(2).StartedAt.Add(300 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, player := range result.Players {
		if player.Rank != nil {
			t.Fatal("expiry ranks must be null")
		}
	}
	if _, err := houseRockets.RestoreSimulation(simulation.State()); err != nil {
		t.Fatalf("expired state cannot restore: %v", err)
	}
}

func TestHouseRocketsCancellationClockAndResultIsolation(t *testing.T) {
	for _, reason := range []houseRockets.EndReason{houseRockets.EndCancelledByUser, houseRockets.EndInsufficientPlayers,
		houseRockets.EndRecoveryFailed, houseRockets.EndCoordinationUnavailable, houseRockets.EndRuntimeOverloaded} {
		params := newHouseRocketsParams(2)
		params.StartedAt = params.StartedAt.In(time.FixedZone("client", 3*3600))
		simulation, err := houseRockets.NewSimulation(params)
		if err != nil {
			t.Fatal(err)
		}
		advanceHouseRockets(t, simulation, 120)
		if err := simulation.Cancel(reason); err != nil {
			t.Fatal(err)
		}
		if err := simulation.Cancel(reason); err != nil {
			t.Fatal("repeated cancellation is not idempotent")
		}
		state := simulation.State()
		if state.StartedAt.Location() != time.UTC || state.Outcome == nil || state.Outcome.Status != houseRockets.ResultCancelled {
			t.Fatal("clock/outcome not normalized")
		}
		result, err := simulation.ProposeResult(params.StartedAt.Add(2 * time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if result.WinnerID != nil || result.EndReason != reason || result.DurationSeconds != 1 || result.StartedAt.Location() != time.UTC || result.EndedAt.Location() != time.UTC {
			t.Fatal("cancelled result is inconsistent")
		}
		for _, player := range result.Players {
			if player.Rank != nil {
				t.Fatal("technical/user cancellation must not award ranks")
			}
		}
		if _, err := simulation.ProposeResult(params.StartedAt.Add(time.Second - time.Nanosecond)); err == nil {
			t.Fatal("finish time is earlier than played simulation time")
		}
		result.Players[0].PlayerID = "fake"
		*result.StartedAt = time.Time{}
		if !reflect.DeepEqual(state, simulation.State()) {
			t.Fatal("result proposal exposes internal memory")
		}
		restored := restoreHouseRocketsTestState(t, state)
		if !reflect.DeepEqual(simulation.Snapshot(), restored.Snapshot()) {
			t.Fatal("cancelled result changed on restore")
		}
	}
	simulation := newHouseRocketsSimulation(t, 2)
	advanceHouseRockets(t, simulation, 1)
	if err := simulation.EliminatePlayers([]string{"p1"}, houseRockets.EliminationForfeit); err != nil {
		t.Fatal(err)
	}
	result, err := simulation.ProposeResult(newHouseRocketsParams(2).StartedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	*result.WinnerID = "fake"
	*result.Players[0].Rank = 99
	*result.Players[0].EliminatedAtTick = 999
	*result.Players[0].EliminationReason = houseRockets.EliminationMembershipRevoked
	next, err := simulation.ProposeResult(newHouseRocketsParams(2).StartedAt.Add(time.Second))
	if err != nil || *next.WinnerID != "p2" || *next.Players[0].Rank != 2 || *next.Players[0].EliminatedAtTick != 1 || *next.Players[0].EliminationReason != houseRockets.EliminationForfeit {
		t.Fatal("result proposal leaked terminal pointers")
	}
}

func TestHouseRocketsStateRoundTripCopiesAndRestoresEffects(t *testing.T) {
	simulation := restoreHouseRocketsTestState(t, houseRocketsFieldState(t, 0, nil))
	advanceHouseRockets(t, simulation, 4)
	state := simulation.State()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var decoded houseRockets.SimulationState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	restored := restoreHouseRocketsTestState(t, decoded)
	if !reflect.DeepEqual(simulation.Snapshot(), restored.Snapshot()) {
		t.Fatal("round trip lost internal effect/contact state")
	}
	advanceHouseRockets(t, simulation, 60)
	advanceHouseRockets(t, restored, 60)
	if !reflect.DeepEqual(simulation.Snapshot(), restored.Snapshot()) {
		t.Fatal("restored effect reapplied or timeline drifted")
	}
	before := simulation.Snapshot()
	snapshot := simulation.Snapshot()
	snapshot.Players[0].WorldX = -999
	snapshot.Players[0].TouchedFieldIDs[0] = "field:99"
	if snapshot.Players[0].SpeedEffect != nil {
		*snapshot.Players[0].SpeedEffect = houseRockets.EffectSlow
	}
	snapshot.Gates[0].Sections[0].LowerY = 999
	snapshot.SpeedFields[0].WorldX = 999
	if !reflect.DeepEqual(before, simulation.Snapshot()) {
		t.Fatal("snapshot exposes internal memory")
	}
	decoded.Players[0].WorldX = -999
	decoded.Players[0].TouchedFieldIDs[0] = "field:99"
	*decoded.Players[0].SpeedEffect = houseRockets.EffectSlow
	if !reflect.DeepEqual(simulation.State(), restored.State()) {
		t.Fatal("restore holds caller-owned memory")
	}
	if err := simulation.EliminatePlayers([]string{"p1"}, houseRockets.EliminationForfeit); err != nil {
		t.Fatal(err)
	}
	if _, err := houseRockets.RestoreSimulation(simulation.State()); err != nil {
		t.Fatal(err)
	}
	terminal := simulation.State()
	*terminal.Outcome.WinnerID = "fake"
	*terminal.Players[0].EliminatedAtTick = -100
	if *simulation.State().Outcome.WinnerID != "p2" || *simulation.State().Players[0].EliminatedAtTick < 0 {
		t.Fatal("terminal state pointers leaked")
	}
}

func TestHouseRocketsRestoreRejectsCorruptState(t *testing.T) {
	base := newHouseRocketsSimulation(t, 2).State()
	changes := []func(*houseRockets.SimulationState){
		func(s *houseRockets.SimulationState) { s.SchemaVersion++ }, func(s *houseRockets.SimulationState) { s.CourseVersion++ },
		func(s *houseRockets.SimulationState) { s.SessionID = "" }, func(s *houseRockets.SimulationState) { s.HouseID = "" },
		func(s *houseRockets.SimulationState) { s.StartedAt = time.Time{} }, func(s *houseRockets.SimulationState) { s.Tick = -1 },
		func(s *houseRockets.SimulationState) { s.Tick = houseRockets.MaximumMatchTicks + 1 }, func(s *houseRockets.SimulationState) { s.Tick = houseRockets.MaximumMatchTicks },
		func(s *houseRockets.SimulationState) { s.CameraX = math.NaN() }, func(s *houseRockets.SimulationState) { s.CameraX = -1 }, func(s *houseRockets.SimulationState) { s.CameraX = 10000 },
		func(s *houseRockets.SimulationState) { s.NextGateIndex = 1 }, func(s *houseRockets.SimulationState) { s.NextGateIndex = 99999 }, func(s *houseRockets.SimulationState) { s.NextGateIndex = 3 },
		func(s *houseRockets.SimulationState) { s.Players = s.Players[:1] }, func(s *houseRockets.SimulationState) { s.Players[1].PlayerID = s.Players[0].PlayerID },
		func(s *houseRockets.SimulationState) { s.Players[0].Color = houseRockets.ColorTeal }, func(s *houseRockets.SimulationState) { s.Players[0].WorldX = math.Inf(1) },
		func(s *houseRockets.SimulationState) { s.Players[0].WorldY = math.NaN() }, func(s *houseRockets.SimulationState) { s.Players[0].WorldY = 0 },
		func(s *houseRockets.SimulationState) { s.Players[0].CourseHeading = math.NaN() }, func(s *houseRockets.SimulationState) { s.Players[0].CourseHeading = 4 },
		func(s *houseRockets.SimulationState) { s.Players[0].WorldX++ }, func(s *houseRockets.SimulationState) { s.Players[0].EffectRemainingSeconds = 1 },
		func(s *houseRockets.SimulationState) { s.Players[0].EffectRemainingSeconds = math.NaN() }, func(s *houseRockets.SimulationState) { s.Players[0].IsAlive = false },
		func(s *houseRockets.SimulationState) { tick := int64(0); s.Players[0].EliminatedAtTick = &tick },
		func(s *houseRockets.SimulationState) {
			s.Outcome = &houseRockets.SimulationOutcome{Status: houseRockets.ResultCompleted, EndReason: houseRockets.EndSimultaneousElimination}
		},
	}
	for index, change := range changes {
		state := newHouseRocketsSimulation(t, 2).State()
		change(&state)
		if _, err := houseRockets.RestoreSimulation(state); !errors.Is(err, houseRockets.ErrInvalidSimulationState) {
			t.Fatalf("corrupt state %d accepted", index)
		}
	}
	if _, err := houseRockets.RestoreSimulation(base); err != nil {
		t.Fatal(err)
	}
	fieldState := houseRocketsFieldState(t, 0, nil)
	for _, ids := range [][]string{{"field:4"}, {"field:0", "field:0"}, {"field:-1"}, {"field:00"}, {"field:999999"}, {"gate:0"}} {
		state := fieldState
		state.Players = append([]houseRockets.PlayerState(nil), fieldState.Players...)
		state.Players[0].TouchedFieldIDs = ids
		if _, err := houseRockets.RestoreSimulation(state); err == nil {
			t.Fatal("corrupt field contact history accepted")
		}
	}
	effect := houseRockets.EffectBoost
	fieldState.Players[0].SpeedEffect = &effect
	for _, remaining := range []float64{-1, 0, 2, math.Inf(1)} {
		fieldState.Players[0].EffectRemainingSeconds = remaining
		if _, err := houseRockets.RestoreSimulation(fieldState); err == nil {
			t.Fatal("corrupt effect duration accepted")
		}
	}
}

func TestHouseRocketsForwardCourseWindowAndRestoreRemainBounded(t *testing.T) {
	simulation := newHouseRocketsSimulation(t, 4)
	for tick := 0; tick < 8000 && simulation.State().Outcome == nil; tick++ {
		snapshot := simulation.Snapshot()
		for index, player := range snapshot.Players {
			if !player.IsAlive {
				continue
			}
			if err := simulation.Steer(player.PlayerID, houseRocketsRouteHeading(player, snapshot.Gates, float64(index)*4-6)); err != nil {
				t.Fatal(err)
			}
		}
		if err := simulation.AdvanceTicks(1); err != nil {
			t.Fatal(err)
		}
		if tick%60 == 0 {
			state := simulation.State()
			restored := restoreHouseRocketsTestState(t, state)
			if !reflect.DeepEqual(simulation.Snapshot(), restored.Snapshot()) {
				t.Fatal("pruned course was not reconstructed identically")
			}
			if len(simulation.Snapshot().Gates) > 6 || len(simulation.Snapshot().SpeedFields) > 5 {
				t.Fatal("active geometry window grows with match duration")
			}
			for _, player := range state.Players {
				if len(player.TouchedFieldIDs) > 5 {
					t.Fatal("expired field contact history grows without bound")
				}
			}
		}
	}
	if simulation.State().NextGateIndex < 8 {
		t.Fatal("test did not traverse a whole course pattern")
	}
}

func houseRocketsRouteHeading(player houseRockets.PlayerState, gates []houseRockets.Gate, laneOffset float64) float64 {
	for _, gate := range gates {
		last := gate.Sections[len(gate.Sections)-1]
		if gate.WorldX+last.OffsetX+30 <= player.WorldX {
			continue
		}
		targetX := math.Min(gate.WorldX+last.OffsetX+30, math.Max(gate.WorldX+gate.Sections[0].OffsetX, player.WorldX+85))
		localX := math.Min(last.OffsetX, math.Max(gate.Sections[0].OffsetX, targetX-gate.WorldX))
		for index := 0; index+1 < len(gate.Sections); index++ {
			left, right := gate.Sections[index], gate.Sections[index+1]
			if localX > right.OffsetX {
				continue
			}
			t := (localX - left.OffsetX) / (right.OffsetX - left.OffsetX)
			lower, upper := left.LowerY+(right.LowerY-left.LowerY)*t, left.UpperY+(right.UpperY-left.UpperY)*t
			center, height := (lower+upper)/2, upper-lower
			safeOffset := math.Min(20, height/2-houseRockets.RocketRadius-14)
			return math.Atan2(center+math.Max(-safeOffset, math.Min(safeOffset, laneOffset))-player.WorldY, math.Max(30, targetX-player.WorldX))
		}
	}
	return 0
}

func TestHouseRocketsNavigationAndEffectGoldenFixtures(t *testing.T) {
	fixture := readHouseRocketsFixture[struct {
		AbsoluteTolerance float64 `json:"absoluteTolerance"`
		NavigationMotions []struct {
			Driver             string `json:"driver"`
			SteerIntervalTicks int64  `json:"steerIntervalTicks"`
			Checkpoints        []struct {
				Tick           int64                      `json:"tick"`
				ElapsedSeconds float64                    `json:"elapsedSeconds"`
				CameraX        float64                    `json:"cameraX"`
				CourseAngle    float64                    `json:"courseAngle"`
				Players        []houseRocketsExpectedBody `json:"players"`
			} `json:"checkpoints"`
		} `json:"navigationMotions"`
	}](t, "houseRocketsSimulation")
	if len(fixture.NavigationMotions) != 3 {
		t.Fatal("missing long-route/boost/slow reference cases")
	}
	for _, entry := range fixture.NavigationMotions {
		t.Run(entry.Driver, func(t *testing.T) {
			simulation := newHouseRocketsSimulation(t, 2)
			steerRoute := func() {
				snapshot := simulation.Snapshot()
				if snapshot.Tick%entry.SteerIntervalTicks != 0 {
					return
				}
				for _, player := range snapshot.Players {
					if err := simulation.Steer(player.PlayerID, houseRocketsRouteHeading(player, snapshot.Gates, 0)); err != nil {
						t.Fatal(err)
					}
				}
			}
			compareCheckpoint := func(index int) {
				expected, snapshot := entry.Checkpoints[index], simulation.Snapshot()
				if expected.Tick != snapshot.Tick {
					t.Fatalf("checkpoint %d: expected tick %d, got %d", index, expected.Tick, snapshot.Tick)
				}
				assertHouseRocketsFloat(t, "elapsedSeconds", expected.ElapsedSeconds, snapshot.ElapsedSeconds, fixture.AbsoluteTolerance)
				assertHouseRocketsFloat(t, "cameraX", expected.CameraX, snapshot.CameraX, fixture.AbsoluteTolerance)
				assertHouseRocketsFloat(t, "courseAngle", expected.CourseAngle, snapshot.CourseAngle, fixture.AbsoluteTolerance)
				assertHouseRocketsBodies(t, expected.Players, snapshot.Players, fixture.AbsoluteTolerance)
				if _, err := houseRockets.RestoreSimulation(simulation.State()); err != nil {
					t.Fatal(err)
				}
			}
			if entry.Driver == "followPassages" {
				for index, checkpoint := range entry.Checkpoints {
					for simulation.State().Tick < checkpoint.Tick {
						steerRoute()
						advanceHouseRockets(t, simulation, 1)
					}
					compareCheckpoint(index)
				}
				return
			}
			if entry.Driver == "boostThenHold" {
				advanceHouseRockets(t, simulation, 294)
			} else {
				for simulation.State().Tick < 1800 {
					steerRoute()
					advanceHouseRockets(t, simulation, 1)
					players := simulation.State().Players
					if players[0].WorldX > 1730 && players[1].WorldX > 1730 {
						break
					}
				}
			}
			compareCheckpoint(0)
			effect := houseRockets.EffectBoost
			if entry.Driver == "slowThenHold" {
				effect = houseRockets.EffectSlow
			}
			var field houseRockets.SpeedField
			for _, candidate := range simulation.Snapshot().SpeedFields {
				if candidate.Effect == effect {
					field = candidate
					break
				}
			}
			if field.ID == "" {
				t.Fatal("reference target field was not generated")
			}
			for range 360 {
				snapshot := simulation.Snapshot()
				for _, player := range snapshot.Players {
					touched := false
					for _, id := range player.TouchedFieldIDs {
						touched = touched || id == field.ID
					}
					if !touched {
						if err := simulation.Steer(player.PlayerID, math.Atan2(field.WorldYAt(snapshot.ElapsedSeconds+0.05)-player.WorldY, field.WorldX-player.WorldX)); err != nil {
							t.Fatal(err)
						}
					}
				}
				advanceHouseRockets(t, simulation, 1)
				allTouched := true
				for _, player := range simulation.State().Players {
					touched := false
					for _, id := range player.TouchedFieldIDs {
						touched = touched || id == field.ID
					}
					allTouched = allTouched && touched
				}
				if allTouched {
					break
				}
			}
			compareCheckpoint(1)
			for _, id := range []string{"p1", "p2"} {
				if err := simulation.Steer(id, math.Pi/2); err != nil {
					t.Fatal(err)
				}
			}
			advanceHouseRockets(t, simulation, 240)
			compareCheckpoint(2)
		})
	}
}

func FuzzHouseRocketsSimulationRestore(f *testing.F) {
	f.Add([]byte{128, 128, 128, 128, 128, 128, 128, 128})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{240, 100, 22, 77, 88, 190, 31, 129, 132, 3, 112, 255})
	f.Fuzz(func(t *testing.T, inputs []byte) {
		if len(inputs) == 0 || len(inputs) > 256 {
			return
		}
		simulation := newHouseRocketsSimulation(t, 2+int(inputs[0])%7)
		for frame, value := range inputs {
			if simulation.State().Outcome != nil {
				break
			}
			for index, player := range simulation.State().Players {
				if !player.IsAlive {
					continue
				}
				heading := float64(int(inputs[(frame+index)%len(inputs)])-128) * math.Pi / 128
				if err := simulation.Steer(player.PlayerID, heading); err != nil {
					t.Fatal(err)
				}
			}
			if err := simulation.AdvanceTicks(1 + int(value)%30); err != nil {
				t.Fatal(err)
			}
			state := simulation.State()
			restored, err := houseRockets.RestoreSimulation(state)
			if err != nil {
				t.Fatalf("valid simulation state cannot restore at tick %d: %v", state.Tick, err)
			}
			if !reflect.DeepEqual(simulation.Snapshot(), restored.Snapshot()) {
				t.Fatal("restore changed a generated world")
			}
		}
	})
}

func BenchmarkHouseRocketsAdvanceEightPlayers(b *testing.B) {
	params := newHouseRocketsParams(8)
	newSimulation := func() *houseRockets.Simulation {
		simulation, err := houseRockets.NewSimulation(params)
		if err != nil {
			b.Fatal(err)
		}
		for _, player := range params.Players {
			if err := simulation.Steer(player.PlayerID, math.Pi/2); err != nil {
				b.Fatal(err)
			}
		}
		return simulation
	}
	simulation, ticks := newSimulation(), int64(0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if ticks == houseRockets.MaximumMatchTicks {
			simulation, ticks = newSimulation(), 0
		}
		if err := simulation.AdvanceTicks(2); err != nil {
			b.Fatal(err)
		}
		ticks += 2
	}
}
