package tests

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"houseflowApi/internal/application/game/games/houseTanks"
)

// Joker spawning is checked by its rules, not by a fixed sequence: clients
// receive the outcome and never reproduce the draws.

func TestHouseTanksJokerBag(t *testing.T) {
	firsts := map[houseTanks.PowerUpKind]int{}
	for seed := range 400 {
		random := houseTanks.NewRandom(houseTanks.MatchSeed(fmt.Sprintf("bag-%d", seed)))
		var bag houseTanks.JokerBag
		var previous houseTanks.PowerUpKind
		for round := range 12 {
			dealt := map[houseTanks.PowerUpKind]bool{}
			for draw := range len(houseTanks.PowerUpKinds()) {
				kind := bag.Next(&random)
				if dealt[kind] {
					t.Fatalf("seed %d bag %d repeats %s", seed, round, kind)
				}
				if kind == previous {
					t.Fatalf("seed %d: %s dealt twice in a row across bags", seed, kind)
				}
				if round == 0 && draw == 0 {
					firsts[kind]++
				}
				dealt[kind], previous = true, kind
				if !bag.Valid() {
					t.Fatalf("dealt bag %+v is not valid", bag)
				}
			}
			if len(dealt) != len(houseTanks.PowerUpKinds()) || len(bag.Queue) != 0 {
				t.Fatal("a bag deals every kind once")
			}
		}
	}
	if len(firsts) != len(houseTanks.PowerUpKinds()) {
		t.Fatalf("every kind can open a match: %v", firsts)
	}
	for kind, count := range firsts {
		if count < 40 {
			t.Fatalf("%s opens only %d of 400 matches", kind, count)
		}
	}
	// A bag stored in a checkpoint deals on unchanged.
	random := houseTanks.NewRandom(7)
	var bag houseTanks.JokerBag
	bag.Next(&random)
	bag.Next(&random)
	encoded, _ := json.Marshal(bag)
	var restored houseTanks.JokerBag
	if json.Unmarshal(encoded, &restored) != nil || !restored.Valid() {
		t.Fatal("bag round trip")
	}
	copied := random
	if bag.Next(&random) != restored.Next(&copied) || random != copied {
		t.Fatal("restored bag deals differently")
	}
	for _, invalid := range []houseTanks.JokerBag{
		{Queue: []houseTanks.PowerUpKind{houseTanks.PowerUpAim}},
		{Queue: []houseTanks.PowerUpKind{houseTanks.PowerUpAim, houseTanks.PowerUpAim}, Last: houseTanks.PowerUpRocket},
		{Queue: []houseTanks.PowerUpKind{houseTanks.PowerUpRocket}, Last: houseTanks.PowerUpRocket},
		{Queue: []houseTanks.PowerUpKind{"laser"}, Last: houseTanks.PowerUpRocket},
		{Last: "laser"},
		{Queue: houseTanks.PowerUpKinds(), Last: houseTanks.PowerUpRocket},
	} {
		if invalid.Valid() {
			t.Fatalf("bag %+v accepted", invalid)
		}
	}
}

func TestHouseTanksJokerSpotRules(t *testing.T) {
	area := houseTanks.JokerArea()
	if area != (houseTanks.Rect{X: 48, Y: 44, Width: 776, Height: 276}) {
		t.Fatalf("joker area = %+v", area)
	}
	center := houseTanks.Vector{X: 436, Y: 182}
	if !houseTanks.ValidJokerSpot(center, nil, nil, nil) {
		t.Fatal("open floor")
	}
	for _, outside := range []houseTanks.Vector{{X: 47.9, Y: 182}, {X: 824.1, Y: 182}, {X: 436, Y: 43.9}, {X: 436, Y: 320.1}} {
		if houseTanks.ValidJokerSpot(outside, nil, nil, nil) {
			t.Fatalf("%v is outside the joker area", outside)
		}
	}
	// Spawn points: max(80, 276 × 0.22) = 80, unscaled.
	startClearance := 80.0
	if houseTanks.ValidJokerSpot(center, []houseTanks.Vector{{X: 436 + startClearance - 0.01, Y: 182}}, nil, nil) ||
		!houseTanks.ValidJokerSpot(center, []houseTanks.Vector{{X: 436 + startClearance + 1e-9, Y: 182}}, nil, nil) {
		t.Fatal("start clearance")
	}
	if houseTanks.ValidJokerSpot(center, nil, []houseTanks.Vector{{X: 436, Y: 231.99}}, nil) ||
		!houseTanks.ValidJokerSpot(center, nil, []houseTanks.Vector{{X: 436, Y: 232}}, nil) {
		t.Fatal("tank clearance")
	}
	cover := houseTanks.Rect{X: 446, Y: 172, Width: 20, Height: 20}
	if houseTanks.ValidJokerSpot(houseTanks.Vector{X: 420, Y: 182}, nil, nil, []houseTanks.Rect{cover}) ||
		!houseTanks.ValidJokerSpot(houseTanks.Vector{X: 419.9, Y: 182}, nil, nil, []houseTanks.Rect{cover}) {
		t.Fatal("cover clearance")
	}
}

// tanksTokenSpotIsValid rebuilds the spot rules from the world state.
func tanksTokenSpotIsValid(world *houseTanks.World, point houseTanks.Vector) bool {
	definition := world.Definition()
	var starts, tanks []houseTanks.Vector
	for _, spawn := range definition.SpawnPoints {
		starts = append(starts, houseTanks.Vector{X: spawn.X * houseTanks.ArenaWidth, Y: spawn.Y * houseTanks.ArenaHeight})
	}
	var cover []houseTanks.Rect
	for _, tank := range world.Tanks {
		if tank.Active {
			tanks = append(tanks, tank.Position)
			continue
		}
		radius := houseTanks.TankRadius * definition.Scale
		cover = append(cover, houseTanks.Rect{X: tank.Position.X - radius, Y: tank.Position.Y - radius, Width: 2 * radius, Height: 2 * radius})
	}
	for _, obstacle := range world.Obstacles {
		if obstacle.Destroyed {
			continue
		}
		authored := definition.Obstacles[obstacle.Index]
		scale := houseTanks.ObstacleBaseScale * houseTanks.GameplayScale * definition.Scale
		height := houseTanks.ArenaHeight * authored.Height * scale
		width := houseTanks.ArenaWidth * authored.Width * scale
		if authored.Style == houseTanks.StyleCrate || authored.Style == houseTanks.StyleRoundTable ||
			authored.Style == houseTanks.StylePillar || authored.Style == houseTanks.StylePlanter {
			width = height
		}
		cos, sin := math.Abs(math.Cos(authored.Rotation)), math.Abs(math.Sin(authored.Rotation))
		boundsWidth, boundsHeight := cos*width+sin*height, sin*width+cos*height
		cover = append(cover, houseTanks.Rect{X: obstacle.Position.X - boundsWidth/2, Y: obstacle.Position.Y - boundsHeight/2, Width: boundsWidth, Height: boundsHeight})
	}
	return houseTanks.ValidJokerSpot(point, starts, tanks, cover)
}

func TestHouseTanksJokerSchedule(t *testing.T) {
	for _, definition := range houseTanks.Maps() {
		for seed := range 8 {
			players := definition.MinPlayers + seed%(definition.MaxPlayers-definition.MinPlayers+1)
			world, err := houseTanks.NewWorld(definition, tanksPlayerIDs(players), 600)
			if err != nil {
				t.Fatal(err)
			}
			// A wreck and a broken crate change where a token may go.
			world.Tanks[0].Active, world.Tanks[0].Armor = false, 0
			for index, obstacle := range definition.Obstacles {
				if obstacle.Style == houseTanks.StyleCrate {
					world.Obstacles[index].Destroyed = true
					break
				}
			}
			world.StartJokers(houseTanks.NewRandom(houseTanks.MatchSeed(fmt.Sprintf("%s-%d", definition.ID, seed))), houseTanks.JokerBag{})
			first := world.Jokers.NextTokenTick
			if first < 600+houseTanks.PowerUpMinimumTicks || first > 600+houseTanks.PowerUpMaximumTicks {
				t.Fatalf("first joker due at %d", first)
			}
			var spawns []int64
			var kinds []houseTanks.PowerUpKind
			var previous *houseTanks.Token
			for world.Tick < 600+7200 {
				due := world.Jokers.NextTokenTick
				world.Step()
				token := world.Token
				if token == previous {
					if world.Tick == due && world.Jokers.NextTokenTick != world.Tick+houseTanks.PowerUpRetryTicks {
						t.Fatal("a missed joker must be retried one second later")
					}
					continue
				}
				previous = token
				if token == nil {
					t.Fatalf("%s: token vanished at %d before its replacement", definition.ID, world.Tick)
				}
				if world.Tick != due || token.ExpiresAtTick != world.Tick+houseTanks.PowerUpTokenLifetimeTicks || !tanksTokenSpotIsValid(world, token.Position) {
					t.Fatalf("%s: token %+v at tick %d (due %d) breaks the rules", definition.ID, token, world.Tick, due)
				}
				interval := world.Jokers.NextTokenTick - world.Tick
				if interval < houseTanks.PowerUpMinimumTicks || interval > houseTanks.PowerUpMaximumTicks {
					t.Fatalf("next joker in %d ticks", interval)
				}
				spawns, kinds = append(spawns, world.Tick), append(kinds, token.Kind)
			}
			// Nobody collects, so every joker is replaced by the next one on time.
			if len(spawns) < 7200/houseTanks.PowerUpMaximumTicks {
				t.Fatalf("%s: only %d jokers in 60 s", definition.ID, len(spawns))
			}
			for index := 1; index < len(kinds); index++ {
				if kinds[index] == kinds[index-1] {
					t.Fatal("the bag never deals one kind twice in a row")
				}
			}
		}
	}
}

func TestHouseTanksJokerScheduleIsDeterministic(t *testing.T) {
	definition, _ := houseTanks.MapByID(houseTanks.MapWarehouse)
	run := func(seed string, checkpointAt int64) []houseTanks.Token {
		world, _ := houseTanks.NewWorld(definition, tanksPlayerIDs(3), 0)
		world.StartJokers(houseTanks.NewRandom(houseTanks.MatchSeed(seed)), houseTanks.JokerBag{})
		var tokens []houseTanks.Token
		for world.Tick < 6000 {
			if world.Tick == checkpointAt {
				// A recovered owner continues from the stored world.
				encoded, err := json.Marshal(world)
				if err != nil {
					t.Fatal(err)
				}
				restored := *world
				restored.Jokers = nil
				if json.Unmarshal(encoded, &restored) != nil || restored.Jokers == nil {
					t.Fatal("world checkpoint")
				}
				world = &restored
			}
			before := world.Token
			world.Step()
			if world.Token != nil && world.Token != before {
				tokens = append(tokens, *world.Token)
			}
		}
		return tokens
	}
	first, again, recovered := run("session-a", -1), run("session-a", -1), run("session-a", 2000)
	if len(first) == 0 || fmt.Sprint(first) != fmt.Sprint(again) || fmt.Sprint(first) != fmt.Sprint(recovered) {
		t.Fatal("one session deals one joker sequence")
	}
	if fmt.Sprint(first) == fmt.Sprint(run("session-b", -1)) {
		t.Fatal("sessions deal different jokers")
	}
}

func TestHouseTanksJokerRetry(t *testing.T) {
	// One piece of cover over the whole arena leaves no valid spot.
	definition, _ := houseTanks.MapByID(houseTanks.MapLivingRoom)
	definition.Obstacles = []houseTanks.MapObstacle{{X: 0.5, Y: 0.5, Width: 3, Height: 3, Style: houseTanks.StyleDivider}}
	world, err := houseTanks.NewWorld(definition, tanksPlayerIDs(2), 0)
	if err != nil {
		t.Fatal(err)
	}
	world.StartJokers(houseTanks.NewRandom(1), houseTanks.JokerBag{})
	due := world.Jokers.NextTokenTick
	for world.Tick < due {
		world.Step()
	}
	if world.Token != nil || world.Jokers.NextTokenTick != due+houseTanks.PowerUpRetryTicks || len(world.Jokers.Bag.Queue) != 0 {
		t.Fatal("a crowded arena retries in one second without dealing")
	}
	// Collected jokers start their effect at once and the floor stays empty
	// until the next one is due.
	open, _ := houseTanks.MapByID(houseTanks.MapLivingRoom)
	world, _ = houseTanks.NewWorld(open, tanksPlayerIDs(2), 0)
	world.StartJokers(houseTanks.NewRandom(2), houseTanks.JokerBag{})
	for world.Token == nil {
		world.Step()
	}
	token, next := *world.Token, world.Jokers.NextTokenTick
	world.Tanks[1].Position = token.Position
	world.Step()
	effect := world.Tanks[1].PowerUp
	if world.Token != nil || effect == nil || effect.Kind != token.Kind || effect.ExpiresAtTick != world.Tick+houseTanks.PowerUpDurationTicks {
		t.Fatal("collection")
	}
	for world.Tick < next-1 {
		world.Step()
		if world.Token != nil {
			t.Fatal("a joker appeared before it was due")
		}
	}
	world.Step()
	if world.Token == nil || world.Token.Kind == token.Kind {
		t.Fatal("the next joker is due on its tick")
	}
	if world.PlaceToken(houseTanks.PowerUpAim, houseTanks.Vector{X: -10, Y: 10}) || world.Grant("seat0", "laser") {
		t.Fatal("invalid joker accepted")
	}
}
