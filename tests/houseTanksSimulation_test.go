package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"reflect"
	"testing"

	"houseflowApi/internal/application/game/games/houseTanks"
)

// The simulation fixture is generated from the Go engine, which is the
// reference: `go test ./tests -run TestHouseTanksSimulationFixture -update-house-tanks`
// rewrites it from the scenarios below. Clients replay every scenario and
// must reach the same checkpoints within the tolerance.
var updateHouseTanksFixtures = flag.Bool("update-house-tanks", false, "rewrite the House Tanks simulation fixture")

const (
	houseTanksFixtureDirectory = "../internal/application/game/games/houseTanks/fixtures/"
	houseTanksSwiftReference   = "../../houseflowApp/FausCrew/shared/Sources/FausCrewCore/Games/HouseTanks/"
)

type tanksScenarioTank struct {
	Seat              int         `json:"seat"`
	X                 float64     `json:"x"`
	Y                 float64     `json:"y"`
	Rotation          float64     `json:"rotation"`
	RotationDirection float64     `json:"rotationDirection"`
	Armor             *int        `json:"armor,omitempty"`
	Wreck             bool        `json:"wreck,omitempty"`
	Controllable      *bool       `json:"controllable,omitempty"`
	FiredAtTick       *int64      `json:"firedAtTick,omitempty"`
	PowerUp           string      `json:"powerUp,omitempty"`
	Velocity          *[2]float64 `json:"velocity,omitempty"`
}

type tanksScenarioToken struct {
	Kind houseTanks.PowerUpKind `json:"kind"`
	X    float64                `json:"x"`
	Y    float64                `json:"y"`
}

// tanksScenarioInput is applied when the world is at Tick, before the step to Tick+1.
type tanksScenarioInput struct {
	Tick   int64  `json:"tick"`
	Seat   int    `json:"seat"`
	Action string `json:"action"`
}

type tanksScenarioTankState struct {
	Seat               int                     `json:"seat"`
	X                  float64                 `json:"x"`
	Y                  float64                 `json:"y"`
	VX                 float64                 `json:"vx"`
	VY                 float64                 `json:"vy"`
	Rotation           float64                 `json:"rotation"`
	RotationDirection  float64                 `json:"rotationDirection"`
	Active             bool                    `json:"active"`
	Controllable       bool                    `json:"controllable"`
	Driving            bool                    `json:"driving"`
	AimLocks           int                     `json:"aimLocks"`
	Armor              int                     `json:"armor"`
	LastFireTick       *int64                  `json:"lastFireTick"`
	ProtectedUntilTick int64                   `json:"protectedUntilTick"`
	PowerUp            *houseTanks.PowerUpKind `json:"powerUp"`
	PowerUpExpiresAt   *int64                  `json:"powerUpExpiresAtTick"`
}

type tanksScenarioCrateState struct {
	Index     int     `json:"index"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	VX        float64 `json:"vx"`
	VY        float64 `json:"vy"`
	Destroyed bool    `json:"destroyed"`
}

type tanksScenarioProjectileState struct {
	ID        int64   `json:"id"`
	OwnerSeat int     `json:"ownerSeat"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	VX        float64 `json:"vx"`
	VY        float64 `json:"vy"`
	Rocket    bool    `json:"rocket"`
	HoldsAim  bool    `json:"holdsAim"`
	Radius    float64 `json:"radius"`
	Age       int64   `json:"age"`
	SpawnTick int64   `json:"spawnTick"`
}

type tanksScenarioEvent struct {
	Tick       int64    `json:"tick"`
	Kind       string   `json:"kind"`
	Seat       *int     `json:"seat,omitempty"`
	SourceSeat *int     `json:"sourceSeat,omitempty"`
	X          *float64 `json:"x,omitempty"`
	Y          *float64 `json:"y,omitempty"`
	Angle      *float64 `json:"angle,omitempty"`
	Armor      *int     `json:"armor,omitempty"`
	Lethal     bool     `json:"lethal,omitempty"`
	CrateIndex *int     `json:"crateIndex,omitempty"`
	PowerUp    string   `json:"powerUp,omitempty"`
}

type tanksScenarioCheckpoint struct {
	Tick        int64                          `json:"tick"`
	Tanks       []tanksScenarioTankState       `json:"tanks"`
	Crates      []tanksScenarioCrateState      `json:"crates"`
	Projectiles []tanksScenarioProjectileState `json:"projectiles"`
	Token       *tanksScenarioToken            `json:"token"`
	// Events happened after the previous checkpoint, up to this tick.
	Events []tanksScenarioEvent `json:"events"`
}

type tanksScenario struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	MapID       houseTanks.MapID          `json:"mapId"`
	Players     int                       `json:"players"`
	Tanks       []tanksScenarioTank       `json:"tanks"`
	Token       *tanksScenarioToken       `json:"token"`
	Inputs      []tanksScenarioInput      `json:"inputs"`
	Checkpoints []tanksScenarioCheckpoint `json:"checkpoints"`
}

type tanksSimulationFixture struct {
	SchemaVersion         int                `json:"schemaVersion"`
	ContractRevision      string             `json:"contractRevision"`
	MapCatalogVersion     int                `json:"mapCatalogVersion"`
	Reference             string             `json:"reference"`
	ReferenceSourceSha256 map[string]string  `json:"referenceSourceSha256"`
	AbsoluteTolerance     float64            `json:"absoluteTolerance"`
	Rules                 string             `json:"rules"`
	Constants             map[string]float64 `json:"constants"`
	Scenarios             []tanksScenario    `json:"scenarios"`
}

func tanksConstants() map[string]float64 {
	return map[string]float64{
		"arenaWidth": houseTanks.ArenaWidth, "arenaHeight": houseTanks.ArenaHeight, "physicsRateHz": houseTanks.PhysicsRateHz,
		"minimumWallInset": houseTanks.MinimumWallInset, "wallInsetRatio": houseTanks.WallInsetRatio,
		"gameplayScale": houseTanks.GameplayScale, "obstacleBaseScale": houseTanks.ObstacleBaseScale,
		"tankRadius": houseTanks.TankRadius, "tankLength": houseTanks.TankLength, "rotationSpeed": houseTanks.RotationSpeed,
		"driveForce": houseTanks.DriveForce, "tankMass": houseTanks.TankMass, "tankDamping": houseTanks.TankDamping,
		"maximumTankSpeed": houseTanks.MaximumTankSpeed, "tankRestitution": houseTanks.TankRestitution,
		"obstacleRestitution": houseTanks.ObstacleRestitution, "crateRestitution": houseTanks.CrateRestitution,
		"crateMass": houseTanks.CrateMass, "crateDamping": houseTanks.CrateDamping, "contactIterations": houseTanks.ContactIterations,
		"shellSpeed": houseTanks.ShellSpeed, "shellRadius": houseTanks.ShellRadius, "shellMuzzleOffset": houseTanks.ShellMuzzleOffset,
		"rocketSpeed": houseTanks.RocketSpeed, "rocketRadius": houseTanks.RocketRadius, "rocketLength": houseTanks.RocketLength,
		"rocketMuzzleOffset": houseTanks.RocketMuzzleOffset, "spawnClearance": houseTanks.SpawnClearance,
		"armorPerRound": houseTanks.ArmorPerRound, "powerUpTokenRadius": houseTanks.PowerUpTokenRadius,
		"fireCooldownTicks": houseTanks.FireCooldownTicks, "rapidFireCooldownTicks": houseTanks.RapidFireCooldownTicks,
		"hitProtectionTicks": houseTanks.HitProtectionTicks, "shellLifetimeTicks": houseTanks.ShellLifetimeTicks,
		"rocketLifetimeTicks": houseTanks.RocketLifetimeTicks, "powerUpDurationTicks": houseTanks.PowerUpDurationTicks,
		"powerUpTokenLifetimeTicks": houseTanks.PowerUpTokenLifetimeTicks,
	}
}

const tanksSimulationRules = "Seats take spawn points in order, face the arena center and sweep counter-clockwise on even seats. " +
	"A scenario's tanks override a seat's pose; firedAtTick marks a barrel as fired at that tick (cooling down), wreck makes a destroyed tank, " +
	"powerUp grants a joker for its full duration from tick 0. Inputs apply when the world is at their tick, before the next step: " +
	"press, release, loseControl (stationary target) and regainControl. Each step runs: tick += 1; expire token and jokers; tanks (drive, " +
	"rapid fire, sweep, damping, speed limit, move); crates (damping, move); three contact passes; joker pickup; shots (muzzle hits of a press " +
	"first); hits in order. Joker dealing (after expiry, before tanks) is off in these scenarios; its rules are tested separately. " +
	"Rotations are not normalized. Players are named seat0…seat7."

func tanksPlayerIDs(count int) []string {
	ids := make([]string, count)
	for seat := range ids {
		ids[seat] = fmt.Sprintf("seat%d", seat)
	}
	return ids
}

func intPointer(value int) *int       { return &value }
func int64Pointer(value int64) *int64 { return &value }
func boolPointer(value bool) *bool    { return &value }

// houseTanksScenarios are the reference cases; positions are chosen in open
// floor of the named map.
func houseTanksScenarios() []tanksScenario {
	press := func(tick int64, seat int) tanksScenarioInput { return tanksScenarioInput{tick, seat, "press"} }
	release := func(tick int64, seat int) tanksScenarioInput { return tanksScenarioInput{tick, seat, "release"} }
	cooling := int64Pointer(0)
	at := func(ticks ...int64) []tanksScenarioCheckpoint {
		checkpoints := make([]tanksScenarioCheckpoint, len(ticks))
		for index, tick := range ticks {
			checkpoints[index].Tick = tick
		}
		return checkpoints
	}
	return []tanksScenario{
		{Name: "idleSweep", Description: "Two tanks at their spawns sweep in opposite directions.", MapID: houseTanks.MapLivingRoom, Players: 2,
			Checkpoints: at(1, 120, 240)},
		{Name: "driveAndRelease", Description: "A press fires and drives along the barrel; the release stops the tank and reverses the sweep.",
			MapID: houseTanks.MapLivingRoom, Players: 2, Tanks: []tanksScenarioTank{{Seat: 0, X: 300, Y: 60, RotationDirection: 1}},
			Inputs: []tanksScenarioInput{press(0, 0), release(90, 0)}, Checkpoints: at(1, 30, 90, 91, 180, 240)},
		{Name: "wallBounce", Description: "A tank drives into the right wall and is held by it; its shell stops at the wall.",
			MapID: houseTanks.MapLivingRoom, Players: 2, Tanks: []tanksScenarioTank{{Seat: 0, X: 700, Y: 60, RotationDirection: 1}},
			Inputs: []tanksScenarioInput{press(0, 0), release(240, 0)}, Checkpoints: at(30, 60, 120, 170, 180, 240)},
		{Name: "headOn", Description: "Two tanks fire at each other, are hit on the same tick, then meet and push against each other.",
			MapID: houseTanks.MapLivingRoom, Players: 2,
			Tanks:  []tanksScenarioTank{{Seat: 0, X: 400, Y: 362, RotationDirection: 1}, {Seat: 1, X: 550, Y: 362, Rotation: math.Pi, RotationDirection: -1}},
			Inputs: []tanksScenarioInput{press(0, 0), press(0, 1), release(160, 0), release(160, 1)}, Checkpoints: at(20, 40, 80, 120, 160, 200)},
		{Name: "cratePush", Description: "A cooling tank drives into a crate and shoves it.",
			MapID: houseTanks.MapLivingRoom, Players: 2, Tanks: []tanksScenarioTank{{Seat: 0, X: 455.2, Y: 160, RotationDirection: 1, FiredAtTick: cooling}},
			Inputs: []tanksScenarioInput{press(0, 0), release(120, 0)}, Checkpoints: at(30, 80, 120, 180)},
		{Name: "shellBreaksCrate", Description: "A shell breaks the crate in front of the tank.",
			MapID: houseTanks.MapLivingRoom, Players: 2, Tanks: []tanksScenarioTank{{Seat: 0, X: 455.2, Y: 160, RotationDirection: 1}},
			Inputs: []tanksScenarioInput{press(0, 0), release(2, 0)}, Checkpoints: at(5, 30)},
		{Name: "muzzleBlocked", Description: "A muzzle inside a crate breaks it at once instead of spawning a shell behind it.",
			MapID: houseTanks.MapLivingRoom, Players: 2, Tanks: []tanksScenarioTank{{Seat: 0, X: 503.2, Y: 160, RotationDirection: 1}},
			Inputs: []tanksScenarioInput{press(0, 0), release(1, 0)}, Checkpoints: at(1, 10)},
		{Name: "rocketKill", Description: "A rocket destroys a full-armor tank, which stays as a wreck.",
			MapID: houseTanks.MapLivingRoom, Players: 2,
			Tanks:  []tanksScenarioTank{{Seat: 0, X: 400, Y: 362, RotationDirection: 1, PowerUp: "rocket"}, {Seat: 1, X: 650, Y: 362, Rotation: math.Pi, RotationDirection: -1}},
			Inputs: []tanksScenarioInput{press(0, 0), release(3, 0)}, Checkpoints: at(30, 60, 90)},
		{Name: "shieldBlocks", Description: "A shield absorbs a rocket without damage.",
			MapID: houseTanks.MapLivingRoom, Players: 2,
			Tanks:  []tanksScenarioTank{{Seat: 0, X: 400, Y: 362, RotationDirection: 1, PowerUp: "rocket"}, {Seat: 1, X: 650, Y: 362, Rotation: math.Pi, RotationDirection: -1, PowerUp: "shield"}},
			Inputs: []tanksScenarioInput{press(0, 0), release(3, 0)}, Checkpoints: at(30, 60, 90)},
		{Name: "spreadProtection", Description: "Three spread pellets reach the target on one tick; hit protection lets only the first count.",
			MapID: houseTanks.MapLivingRoom, Players: 2,
			Tanks:  []tanksScenarioTank{{Seat: 0, X: 400, Y: 362, RotationDirection: 1, PowerUp: "spreadShot"}, {Seat: 1, X: 462, Y: 362, Rotation: math.Pi, RotationDirection: -1, FiredAtTick: cooling}},
			Inputs: []tanksScenarioInput{press(0, 0), release(1, 0)}, Checkpoints: at(3, 10, 60)},
		{Name: "aimLock", Description: "An aimed shot holds the heading until it lands on the far wall.",
			MapID: houseTanks.MapLivingRoom, Players: 2, Tanks: []tanksScenarioTank{{Seat: 0, X: 300, Y: 60, RotationDirection: 1, PowerUp: "aim"}},
			Inputs: []tanksScenarioInput{press(0, 0), release(2, 0)}, Checkpoints: at(100, 120, 129, 240)},
		{Name: "rapidFire", Description: "Rapid fire shoots every 20 ticks while held.",
			MapID: houseTanks.MapLivingRoom, Players: 2, Tanks: []tanksScenarioTank{{Seat: 0, X: 300, Y: 60, RotationDirection: 1, PowerUp: "rapidFire"}},
			Inputs: []tanksScenarioInput{press(0, 0), release(61, 0)}, Checkpoints: at(20, 40, 61, 90)},
		{Name: "stationaryTarget", Description: "A tank that loses control stops turning, is shoved by another tank and turns again once control returns.",
			MapID: houseTanks.MapLivingRoom, Players: 2,
			Tanks:       []tanksScenarioTank{{Seat: 0, X: 400, Y: 362, RotationDirection: 1, FiredAtTick: cooling}, {Seat: 1, X: 450, Y: 362, Rotation: math.Pi, RotationDirection: -1}},
			Inputs:      []tanksScenarioInput{{0, 1, "loseControl"}, press(0, 0), release(80, 0), {100, 1, "regainControl"}},
			Checkpoints: at(20, 40, 80, 100, 140)},
		{Name: "wreckBlocks", Description: "A wreck stops a shell without a hit and deflects a driving tank.",
			MapID: houseTanks.MapLivingRoom, Players: 2,
			Tanks:  []tanksScenarioTank{{Seat: 0, X: 400, Y: 362, RotationDirection: 1}, {Seat: 1, X: 510, Y: 365, Rotation: math.Pi, RotationDirection: -1, Wreck: true}},
			Inputs: []tanksScenarioInput{press(0, 0), release(150, 0)}, Checkpoints: at(20, 60, 120, 150)},
		{Name: "jokerPickup", Description: "A driving tank collects the joker on the floor.",
			MapID: houseTanks.MapLivingRoom, Players: 2, Tanks: []tanksScenarioTank{{Seat: 0, X: 300, Y: 60, RotationDirection: 1, FiredAtTick: cooling}},
			Token: &tanksScenarioToken{Kind: houseTanks.PowerUpShield, X: 345, Y: 60}, Inputs: []tanksScenarioInput{press(0, 0), release(90, 0)},
			Checkpoints: at(10, 60, 90)},
		{Name: "jokerExpiry", Description: "A joker effect and an untouched token both run out on their expiry tick.",
			MapID: houseTanks.MapLivingRoom, Players: 2, Tanks: []tanksScenarioTank{{Seat: 0, X: 300, Y: 60, RotationDirection: 1, PowerUp: "shield"}},
			Token: &tanksScenarioToken{Kind: houseTanks.PowerUpAim, X: 650, Y: 362}, Checkpoints: at(959, 960, 1439, 1440)},
		{Name: "crowdedBasement", Description: "Eight seats on the scaled crowded map; every seat fires toward the center.",
			MapID: houseTanks.MapBasement, Players: 8,
			Inputs: []tanksScenarioInput{press(0, 0), press(0, 1), press(0, 2), press(0, 3), press(0, 4), press(0, 5), press(0, 6), press(0, 7),
				release(30, 0), release(30, 1), release(30, 2), release(30, 3), release(30, 4), release(30, 5), release(30, 6), release(30, 7)},
			Checkpoints: at(1, 30, 60, 120, 240)},
	}
}

// runTanksScenario plays a scenario and records its checkpoints.
func runTanksScenario(t *testing.T, scenario tanksScenario) []tanksScenarioCheckpoint {
	t.Helper()
	definition, found := houseTanks.MapByID(scenario.MapID)
	if !found {
		t.Fatalf("unknown map %s", scenario.MapID)
	}
	ids := tanksPlayerIDs(scenario.Players)
	world, err := houseTanks.NewWorld(definition, ids, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, setup := range scenario.Tanks {
		tank := &world.Tanks[setup.Seat]
		tank.Position, tank.Rotation, tank.RotationDirection = houseTanks.Vector{X: setup.X, Y: setup.Y}, setup.Rotation, setup.RotationDirection
		if setup.Velocity != nil {
			tank.Velocity = houseTanks.Vector{X: setup.Velocity[0], Y: setup.Velocity[1]}
		}
		if setup.Armor != nil {
			tank.Armor = *setup.Armor
		}
		if setup.FiredAtTick != nil {
			tank.HasFired, tank.LastFireTick = true, *setup.FiredAtTick
		}
		if setup.PowerUp != "" && !world.Grant(tank.PlayerID, houseTanks.PowerUpKind(setup.PowerUp)) {
			t.Fatal("grant failed")
		}
		if setup.Controllable != nil {
			world.SetControllable(tank.PlayerID, *setup.Controllable)
		}
		if setup.Wreck {
			tank.Active, tank.Armor = false, 0
		}
	}
	if scenario.Token != nil && !world.PlaceToken(scenario.Token.Kind, houseTanks.Vector{X: scenario.Token.X, Y: scenario.Token.Y}) {
		t.Fatal("token placement failed")
	}
	last := scenario.Checkpoints[len(scenario.Checkpoints)-1].Tick
	recorded := make([]tanksScenarioCheckpoint, 0, len(scenario.Checkpoints))
	var events []tanksScenarioEvent
	next := 0
	for {
		for _, input := range scenario.Inputs {
			if input.Tick != world.Tick {
				continue
			}
			id := ids[input.Seat]
			switch input.Action {
			case "press":
				world.Press(id)
			case "release":
				world.Release(id)
			case "loseControl":
				world.SetControllable(id, false)
			case "regainControl":
				world.SetControllable(id, true)
			default:
				t.Fatalf("unknown action %s", input.Action)
			}
		}
		if world.Tick == last {
			break
		}
		world.Step()
		events = append(events, tanksEvents(world.DrainEvents())...)
		if world.Tick == scenario.Checkpoints[next].Tick {
			checkpoint := tanksCheckpoint(world)
			checkpoint.Events, events = events, nil
			if checkpoint.Events == nil {
				checkpoint.Events = []tanksScenarioEvent{}
			}
			recorded = append(recorded, checkpoint)
			next++
		}
	}
	return recorded
}

func tanksSeat(id string) *int {
	var seat int
	if _, err := fmt.Sscanf(id, "seat%d", &seat); err != nil {
		return nil
	}
	return &seat
}

func tanksEvents(events []houseTanks.WorldEvent) []tanksScenarioEvent {
	converted := make([]tanksScenarioEvent, 0, len(events))
	for _, event := range events {
		item := tanksScenarioEvent{Tick: event.Tick, Kind: string(event.Kind), Angle: event.Angle, Armor: event.Armor, Lethal: event.Lethal,
			CrateIndex: event.CrateIndex, PowerUp: string(event.PowerUp)}
		if event.PlayerID != "" {
			item.Seat = tanksSeat(event.PlayerID)
		}
		if event.SourceID != "" {
			item.SourceSeat = tanksSeat(event.SourceID)
		}
		if event.Position != nil {
			x, y := event.Position.X, event.Position.Y
			item.X, item.Y = &x, &y
		}
		converted = append(converted, item)
	}
	return converted
}

func tanksCheckpoint(world *houseTanks.World) tanksScenarioCheckpoint {
	checkpoint := tanksScenarioCheckpoint{Tick: world.Tick, Tanks: []tanksScenarioTankState{}, Crates: []tanksScenarioCrateState{}, Projectiles: []tanksScenarioProjectileState{}}
	for _, tank := range world.Tanks {
		state := tanksScenarioTankState{Seat: tank.Seat, X: tank.Position.X, Y: tank.Position.Y, VX: tank.Velocity.X, VY: tank.Velocity.Y,
			Rotation: tank.Rotation, RotationDirection: tank.RotationDirection, Active: tank.Active, Controllable: tank.Controllable,
			Driving: tank.Driving, AimLocks: tank.AimLocks, Armor: tank.Armor, ProtectedUntilTick: tank.ProtectedUntilTick}
		if tank.HasFired {
			state.LastFireTick = int64Pointer(tank.LastFireTick)
		}
		if tank.PowerUp != nil {
			kind, expires := tank.PowerUp.Kind, tank.PowerUp.ExpiresAtTick
			state.PowerUp, state.PowerUpExpiresAt = &kind, &expires
		}
		checkpoint.Tanks = append(checkpoint.Tanks, state)
	}
	for _, obstacle := range world.Obstacles {
		if world.Definition().Obstacles[obstacle.Index].Style == houseTanks.StyleCrate {
			checkpoint.Crates = append(checkpoint.Crates, tanksScenarioCrateState{Index: obstacle.Index, X: obstacle.Position.X, Y: obstacle.Position.Y,
				VX: obstacle.Velocity.X, VY: obstacle.Velocity.Y, Destroyed: obstacle.Destroyed})
		}
	}
	for _, projectile := range world.Projectiles {
		checkpoint.Projectiles = append(checkpoint.Projectiles, tanksScenarioProjectileState{ID: projectile.ID, OwnerSeat: *tanksSeat(projectile.OwnerID),
			X: projectile.Position.X, Y: projectile.Position.Y, VX: projectile.Velocity.X, VY: projectile.Velocity.Y, Rocket: projectile.Rocket,
			HoldsAim: projectile.HoldsAim, Radius: projectile.Radius, Age: projectile.Age, SpawnTick: projectile.SpawnTick})
	}
	if token := world.Token; token != nil {
		checkpoint.Token = &tanksScenarioToken{Kind: token.Kind, X: token.Position.X, Y: token.Position.Y}
	}
	return checkpoint
}

func tanksReferenceHashes(t *testing.T) map[string]string {
	t.Helper()
	hashes := map[string]string{}
	for _, name := range []string{"HouseTanksWorld.swift", "HouseTanksGeometry.swift", "HouseTanksPowerUpRules.swift", "HouseTanksBotMatch.swift", "HouseTanksMapCatalog.swift"} {
		data, err := os.ReadFile(houseTanksSwiftReference + name)
		if err != nil {
			t.Fatalf("Swift reference is needed to update the fixture: %v", err)
		}
		sum := sha256.Sum256(data)
		hashes[name] = hex.EncodeToString(sum[:])
	}
	return hashes
}

func TestHouseTanksSimulationFixture(t *testing.T) {
	path := houseTanksFixtureDirectory + "houseTanksSimulation.json"
	if *updateHouseTanksFixtures {
		fixture := tanksSimulationFixture{SchemaVersion: 1, ContractRevision: houseTanks.ContractRevision, MapCatalogVersion: houseTanks.MapCatalogVersion,
			Reference:             "Go port of houseflowApp/FausCrew/shared/Sources/FausCrewCore/Games/HouseTanks (the Go engine is the reference)",
			ReferenceSourceSha256: tanksReferenceHashes(t), AbsoluteTolerance: 1e-6, Rules: tanksSimulationRules, Constants: tanksConstants()}
		for _, scenario := range houseTanksScenarios() {
			scenario.Checkpoints = runTanksScenario(t, scenario)
			fixture.Scenarios = append(fixture.Scenarios, scenario)
		}
		encoded, err := json.MarshalIndent(fixture, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fixture := readHouseTanksFixture[tanksSimulationFixture](t, "houseTanksSimulation")
	if fixture.SchemaVersion != 1 || fixture.ContractRevision != houseTanks.ContractRevision || fixture.MapCatalogVersion != houseTanks.MapCatalogVersion ||
		fixture.AbsoluteTolerance != 1e-6 || fixture.Rules != tanksSimulationRules || !reflect.DeepEqual(fixture.Constants, tanksConstants()) {
		t.Fatal("simulation fixture header or constants changed; regenerate it with -update-house-tanks")
	}
	expected := houseTanksScenarios()
	if len(fixture.Scenarios) != len(expected) {
		t.Fatal("scenario coverage changed; regenerate the fixture")
	}
	for index, scenario := range fixture.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			spec := expected[index]
			stored := scenario
			stored.Checkpoints = nil
			for _, checkpoint := range spec.Checkpoints {
				stored.Checkpoints = append(stored.Checkpoints, tanksScenarioCheckpoint{Tick: checkpoint.Tick})
			}
			if !reflect.DeepEqual(roundTripJSON(t, stored), roundTripJSON(t, spec)) {
				t.Fatal("scenario setup differs from the code; regenerate the fixture")
			}
			actual := runTanksScenario(t, scenario)
			compareTanksJSON(t, "", roundTripJSON(t, scenario.Checkpoints), roundTripJSON(t, actual), fixture.AbsoluteTolerance)
		})
	}
}

func roundTripJSON(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// compareTanksJSON compares decoded JSON, numbers within tolerance.
func compareTanksJSON(t *testing.T, path string, expected, actual any, tolerance float64) {
	t.Helper()
	switch expectedValue := expected.(type) {
	case float64:
		actualValue, ok := actual.(float64)
		if !ok || math.Abs(expectedValue-actualValue) > tolerance {
			t.Fatalf("%s = %v, want %v", path, actual, expected)
		}
	case []any:
		actualValue, ok := actual.([]any)
		if !ok || len(actualValue) != len(expectedValue) {
			t.Fatalf("%s = %v, want %v", path, actual, expected)
		}
		for index := range expectedValue {
			compareTanksJSON(t, fmt.Sprintf("%s[%d]", path, index), expectedValue[index], actualValue[index], tolerance)
		}
	case map[string]any:
		actualValue, ok := actual.(map[string]any)
		if !ok || len(actualValue) != len(expectedValue) {
			t.Fatalf("%s = %v, want %v", path, actual, expected)
		}
		for key := range expectedValue {
			compareTanksJSON(t, path+"."+key, expectedValue[key], actualValue[key], tolerance)
		}
	default:
		if !reflect.DeepEqual(expected, actual) {
			t.Fatalf("%s = %v, want %v", path, actual, expected)
		}
	}
}

// The fixture is generated, so its behaviour is pinned here against the
// briefing's numbers independently of the engine.
func TestHouseTanksSimulationMatchesBriefing(t *testing.T) {
	fixture := readHouseTanksFixture[tanksSimulationFixture](t, "houseTanksSimulation")
	scenarios := map[string]tanksScenario{}
	for _, scenario := range fixture.Scenarios {
		scenarios[scenario.Name] = scenario
	}
	checkpoint := func(name string, tick int64) tanksScenarioCheckpoint {
		for _, checkpoint := range scenarios[name].Checkpoints {
			if checkpoint.Tick == tick {
				return checkpoint
			}
		}
		t.Fatalf("%s has no checkpoint at %d", name, tick)
		return tanksScenarioCheckpoint{}
	}
	near := func(label string, actual, expected float64) {
		t.Helper()
		if math.Abs(actual-expected) > 1e-9 {
			t.Fatalf("%s = %v, want %v", label, actual, expected)
		}
	}
	dt := 1.0 / 120
	// Idle sweep: 1.5 rad/s in opposite directions from the spawn headings.
	definition, _ := houseTanks.MapByID(houseTanks.MapLivingRoom)
	for _, seat := range []int{0, 1} {
		spawn := definition.SpawnPoints[seat]
		start := math.Atan2(houseTanks.ArenaHeight/2-spawn.Y*houseTanks.ArenaHeight, houseTanks.ArenaWidth/2-spawn.X*houseTanks.ArenaWidth)
		direction := 1.0 - 2*float64(seat)
		near("idle rotation", checkpoint("idleSweep", 240).Tanks[seat].Rotation, start+direction*1.5*240*dt)
	}
	// Driving: v += 288 dt, then damping, never above 245; the release stops
	// the tank, reverses the sweep and the barrel fired once at tick 0.
	speed := 0.0
	for range 30 {
		speed = math.Min((speed+288*dt)/(1+dt*1.8), 245)
	}
	drive := checkpoint("driveAndRelease", 30).Tanks[0]
	near("drive speed", drive.VX, speed)
	if drive.VY != 0 || !drive.Driving || drive.LastFireTick == nil || *drive.LastFireTick != 0 {
		t.Fatal("driving tank state")
	}
	released := checkpoint("driveAndRelease", 91).Tanks[0]
	if released.Driving || released.VX != 0 || released.RotationDirection != -1 {
		t.Fatal("release must stop the tank and reverse the sweep")
	}
	// The shell leaves 15 points ahead at 510 points per second.
	shell := checkpoint("driveAndRelease", 1).Projectiles[0]
	near("shell x", shell.X, 300+15+510*dt)
	if shell.Radius != 1.8 || shell.SpawnTick != 0 {
		t.Fatal("shell shape")
	}
	// Head-on: both tanks are hit on the same tick and lose one armor.
	hitTicks := map[int64]int{}
	for _, tick := range []int64{20, 40} {
		for _, event := range checkpoint("headOn", tick).Events {
			if event.Kind == "hit" {
				hitTicks[event.Tick]++
			}
		}
	}
	if len(hitTicks) != 1 || checkpoint("headOn", 40).Tanks[0].Armor != 4 || checkpoint("headOn", 40).Tanks[1].Armor != 4 {
		t.Fatalf("head-on shots must both land on one tick: %v", hitTicks)
	}
	// They then meet and never overlap.
	for _, recorded := range scenarios["headOn"].Checkpoints {
		if gap := recorded.Tanks[1].X - recorded.Tanks[0].X; gap < 2*9-1e-6 {
			t.Fatalf("tanks overlap at %d: gap %v", recorded.Tick, gap)
		}
	}
	if gap := checkpoint("headOn", 160).Tanks[1].X - checkpoint("headOn", 160).Tanks[0].X; gap > 30 {
		t.Fatalf("head-on tanks never met: gap %v", gap)
	}
	// The wall holds a tank pushing into it: frame inset max(16, 400 × 0.035)
	// = 16 plus the radius.
	wall := houseTanks.ArenaWidth - 16 - 9
	for _, recorded := range scenarios["wallBounce"].Checkpoints {
		if recorded.Tanks[0].X > wall+1e-6 {
			t.Fatalf("tank passed the wall at %d", recorded.Tick)
		}
	}
	if checkpoint("wallBounce", 240).Tanks[0].X < wall-9 || len(checkpoint("wallBounce", 60).Projectiles) != 0 {
		t.Fatal("tank must reach the wall and its shell stop there")
	}
	// A rocket kills a full-armor tank, which becomes a wreck.
	killed := checkpoint("rocketKill", 90).Tanks[1]
	if killed.Active || killed.Armor != 0 {
		t.Fatal("rocket must destroy the tank")
	}
	shielded := checkpoint("shieldBlocks", 90).Tanks[1]
	if !shielded.Active || shielded.Armor != 5 {
		t.Fatal("shield must absorb the rocket")
	}
	// Spread: several pellets, one armor because of hit protection.
	spread := checkpoint("spreadProtection", 10)
	pellets := 0
	for _, event := range append(checkpoint("spreadProtection", 3).Events, spread.Events...) {
		if event.Kind == "hit" {
			pellets++
		}
	}
	if spread.Tanks[1].Armor != 4 || pellets != 1 {
		t.Fatalf("spread protection: armor %d, applied hits %d", spread.Tanks[1].Armor, pellets)
	}
	// Aim lock: the heading holds until the shell lands, then sweeps again.
	if checkpoint("aimLock", 100).Tanks[0].Rotation != 0 || checkpoint("aimLock", 100).Tanks[0].AimLocks != 1 || checkpoint("aimLock", 240).Tanks[0].AimLocks != 0 ||
		checkpoint("aimLock", 240).Tanks[0].Rotation >= 0 {
		t.Fatal("aim lock")
	}
	// Rapid fire: shots at ticks 0, 20, 40 and 60 while held.
	fired := 0
	for _, tick := range []int64{20, 40, 61} {
		for _, event := range checkpoint("rapidFire", tick).Events {
			if event.Kind == "fired" {
				fired++
			}
		}
	}
	if fired != 4 || *checkpoint("rapidFire", 61).Tanks[0].LastFireTick != 60 {
		t.Fatalf("rapid fire shots = %d", fired)
	}
	// Stationary target: no turning, shoved, turns again after control returns.
	stationary := checkpoint("stationaryTarget", 80).Tanks[1]
	if stationary.Controllable || stationary.Rotation != math.Pi || stationary.X <= 450 {
		t.Fatal("stationary target must hold its heading and be shoved")
	}
	if checkpoint("stationaryTarget", 140).Tanks[1].Rotation == math.Pi || !checkpoint("stationaryTarget", 140).Tanks[1].Controllable {
		t.Fatal("control must return")
	}
	// Wreck: no hit event, the shell stops at it and the driving tank slides off it.
	if len(checkpoint("wreckBlocks", 20).Projectiles) != 0 || checkpoint("wreckBlocks", 150).Tanks[0].Y == 362 ||
		checkpoint("wreckBlocks", 150).Tanks[1].X != 510 {
		t.Fatal("wreck must stop the shell and deflect the tank without moving")
	}
	for _, recorded := range scenarios["wreckBlocks"].Checkpoints {
		for _, event := range recorded.Events {
			if event.Kind == "hit" || event.Kind == "eliminated" {
				t.Fatal("a wreck is not hit")
			}
		}
	}
	// Jokers: pickup within 25 points, and expiry on the exact tick.
	if checkpoint("jokerPickup", 10).Token == nil {
		t.Fatal("joker picked up too early")
	}
	picked := checkpoint("jokerPickup", 90)
	if picked.Token != nil || picked.Tanks[0].PowerUp == nil || *picked.Tanks[0].PowerUp != houseTanks.PowerUpShield {
		t.Fatal("joker pickup")
	}
	if checkpoint("jokerExpiry", 959).Tanks[0].PowerUp == nil || checkpoint("jokerExpiry", 960).Tanks[0].PowerUp != nil ||
		checkpoint("jokerExpiry", 1439).Token == nil || checkpoint("jokerExpiry", 1440).Token != nil {
		t.Fatal("joker expiry ticks")
	}
	// Crowded map: bodies and shots shrink by the map scale, speeds do not.
	crowded := checkpoint("crowdedBasement", 1)
	if len(crowded.Tanks) != 8 || len(crowded.Projectiles) == 0 {
		t.Fatal("crowded basement")
	}
	for _, projectile := range crowded.Projectiles {
		near("scaled shell radius", projectile.Radius, 1.8*0.72)
		near("shell speed", math.Hypot(projectile.VX, projectile.VY), 510)
	}
	// Crates: pushed by a cooling tank, broken by a shell or a blocked muzzle.
	// The pushed crate is the third one, obstacle 8 of the living room.
	crateX := definition.Obstacles[8].X * houseTanks.ArenaWidth
	if pushed := checkpoint("cratePush", 120).Crates; pushed[2].Destroyed || pushed[2].X <= crateX || checkpoint("cratePush", 30).Crates[2].X != crateX {
		t.Fatal("crate push")
	}
	if !checkpoint("shellBreaksCrate", 30).Crates[2].Destroyed || !checkpoint("muzzleBlocked", 1).Crates[2].Destroyed || len(checkpoint("muzzleBlocked", 1).Projectiles) != 0 {
		t.Fatal("crate breaking")
	}
}

func TestHouseTanksMapCatalog(t *testing.T) {
	if err := houseTanks.ValidateMapCatalog(); err != nil {
		t.Fatal(err)
	}
	maps := houseTanks.Maps()
	ids := make([]houseTanks.MapID, 0, len(maps))
	for _, definition := range maps {
		ids = append(ids, definition.ID)
		if definition.ID == houseTanks.MapBasement {
			if definition.Scale != 0.72 || definition.MinPlayers != 6 || definition.MaxPlayers != 8 || len(definition.SpawnPoints) != 8 || definition.Version != 1 {
				t.Fatalf("basement = %+v", definition)
			}
			continue
		}
		// Catalog 2 moved courtyard's fifth spawn off the planter at 872 × 400.
		version := 3
		if definition.ID == houseTanks.MapCourtyard {
			version = 4
		}
		if definition.Scale != 1 || definition.MinPlayers != 2 || definition.MaxPlayers != 5 || len(definition.SpawnPoints) != 5 || definition.Version != version {
			t.Fatalf("standard map %s = scale %v players %d-%d spawns %d version %d", definition.ID, definition.Scale, definition.MinPlayers, definition.MaxPlayers, len(definition.SpawnPoints), definition.Version)
		}
		for _, spawn := range definition.SpawnPoints {
			if spawn.Y > 0.70 {
				t.Fatal("no spawn under the top HUD row")
			}
		}
	}
	if !reflect.DeepEqual(ids, []houseTanks.MapID{houseTanks.MapLivingRoom, houseTanks.MapCorridors, houseTanks.MapWarehouse, houseTanks.MapCourtyard, houseTanks.MapBasement}) {
		t.Fatalf("catalog order = %v", ids)
	}
	// The catalog keeps every authored Swift value; only seats and ranges change.
	var catalog struct {
		ReferenceSourceSha256 string `json:"referenceSourceSha256"`
	}
	data, err := os.ReadFile(houseTanksFixtureDirectory + "houseTanksMaps.json")
	if err != nil || json.Unmarshal(data, &catalog) != nil || len(catalog.ReferenceSourceSha256) != 64 {
		t.Fatal("map catalog must name the Swift source it was transcribed from")
	}
	if swift, err := os.ReadFile(houseTanksSwiftReference + "HouseTanksMapCatalog.swift"); err == nil {
		sum := sha256.Sum256(swift)
		if hex.EncodeToString(sum[:]) != catalog.ReferenceSourceSha256 {
			t.Log("HouseTanksMapCatalog.swift changed since the catalog was transcribed; review the map fixture")
		}
	}
	// Map choice depends on the roster size and the seed only.
	for players := houseTanks.MinimumPlayers; players <= houseTanks.MaximumPlayers; players++ {
		seen := map[houseTanks.MapID]bool{}
		for index := range 200 {
			random := houseTanks.NewRandom(houseTanks.MatchSeed(fmt.Sprintf("session-%d", index)))
			definition, err := houseTanks.SelectMap(&random, players)
			if err != nil || !definition.Supports(players) {
				t.Fatalf("%d players: %v", players, err)
			}
			seen[definition.ID] = true
		}
		if players >= 6 && (len(seen) != 1 || !seen[houseTanks.MapBasement]) {
			t.Fatalf("crowded matches use the basement, got %v", seen)
		}
		if players <= 5 && (len(seen) != 4 || seen[houseTanks.MapBasement]) {
			t.Fatalf("standard matches use all four standard maps, got %v", seen)
		}
	}
	first, second := houseTanks.NewRandom(houseTanks.MatchSeed("same")), houseTanks.NewRandom(houseTanks.MatchSeed("same"))
	a, _ := houseTanks.SelectMap(&first, 3)
	b, _ := houseTanks.SelectMap(&second, 3)
	if a.ID != b.ID || houseTanks.SeedText(houseTanks.MatchSeed("same")) != houseTanks.SeedText(houseTanks.MatchSeed("same")) || len(houseTanks.SeedText(1)) != 16 {
		t.Fatal("a session always draws the same map and seed")
	}
	if _, err := houseTanks.SelectMap(&first, 9); err == nil {
		t.Fatal("nine players have no map")
	}
	// SplitMix64 keeps the Swift reference sequence.
	random := houseTanks.NewRandom(0)
	if value := random.Next(); value != 0xE220A8397B1DCDAF {
		t.Fatalf("SplitMix64 first value = %x", value)
	}
}

func TestHouseTanksWorldRejectsBadSeating(t *testing.T) {
	definition, _ := houseTanks.MapByID(houseTanks.MapLivingRoom)
	for _, ids := range [][]string{nil, {"a", "a"}, {"a", ""}, tanksPlayerIDs(6)} {
		if _, err := houseTanks.NewWorld(definition, ids, 0); err == nil {
			t.Fatalf("seating %v accepted", ids)
		}
	}
	world, err := houseTanks.NewWorld(definition, tanksPlayerIDs(5), 7)
	if err != nil || world.Tick != 7 || len(world.Tanks) != 5 {
		t.Fatal("five players fit a standard map")
	}
	for seat, tank := range world.Tanks {
		if tank.Seat != seat || tank.Armor != houseTanks.ArmorPerRound || !tank.Active || !tank.Controllable || tank.RotationDirection != 1-2*float64(seat%2) {
			t.Fatalf("seat %d = %+v", seat, tank)
		}
	}
	// A stationary target cannot press; a wreck cannot be granted a joker.
	world.SetControllable("seat1", false)
	if world.Press("seat1") || world.Tanks[1].Driving {
		t.Fatal("stationary target pressed")
	}
	world.Tanks[2].Active = false
	if world.Grant("seat2", houseTanks.PowerUpRocket) || world.SetRotation("seat0", math.NaN()) {
		t.Fatal("invalid world change accepted")
	}
}
