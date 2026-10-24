package houseTanks

import "math"

// Joker spawning follows HouseTanksPowerUpRules.swift with one approved
// change: a joker due while another is still on the floor replaces it, so
// the schedule does not depend on whether anyone collected the last one.
// Clients do not reproduce the draws; the snapshot carries the outcome.

func (kind PowerUpKind) valid() bool {
	switch kind {
	case PowerUpRocket, PowerUpRapidFire, PowerUpSpreadShot, PowerUpShield, PowerUpAim:
		return true
	}
	return false
}

// JokerBag hands out every kind once per bag in a fresh order; a new bag
// never starts with the kind the previous one ended on. It carries across
// the rounds of a match.
type JokerBag struct {
	Queue []PowerUpKind `json:"queue"`
	Last  PowerUpKind   `json:"last,omitempty"`
}

func (bag *JokerBag) Next(random *Random) PowerUpKind {
	if len(bag.Queue) == 0 {
		queue := PowerUpKinds()
		for index := len(queue) - 1; index > 0; index-- {
			other := random.Below(index + 1)
			queue[index], queue[other] = queue[other], queue[index]
		}
		if queue[0] == bag.Last {
			other := 1 + random.Below(len(queue)-1)
			queue[0], queue[other] = queue[other], queue[0]
		}
		bag.Queue = queue
	}
	kind := bag.Queue[0]
	bag.Queue = append([]PowerUpKind(nil), bag.Queue[1:]...)
	bag.Last = kind
	return kind
}

// Valid reports whether a restored bag could have been dealt by Next.
func (bag JokerBag) Valid() bool {
	if bag.Last == "" && len(bag.Queue) > 0 || bag.Last != "" && !bag.Last.valid() || len(bag.Queue) >= len(PowerUpKinds()) {
		return false
	}
	seen := map[PowerUpKind]bool{bag.Last: bag.Last != ""}
	for _, kind := range bag.Queue {
		if !kind.valid() || seen[kind] {
			return false
		}
		seen[kind] = true
	}
	return true
}

// JokerSchedule is a world's joker dealer. The generator and the bag belong
// to the match and move to the next round's world; the due tick is per round.
type JokerSchedule struct {
	Random        Random   `json:"random"`
	Bag           JokerBag `json:"bag"`
	NextTokenTick int64    `json:"nextTokenTick"`
}

// spawnInterval is uniform over 7…11 s in whole ticks.
func (random *Random) spawnInterval() int64 {
	return PowerUpMinimumTicks + int64(random.Below(PowerUpMaximumTicks-PowerUpMinimumTicks+1))
}

// JokerArea is where tokens may appear: clear of the walls and the top HUD
// row. It does not shrink with the map scale.
func JokerArea() Rect {
	return Rect{PowerUpAreaInsetX, PowerUpAreaBottomInset, ArenaWidth - 2*PowerUpAreaInsetX,
		ArenaHeight - PowerUpAreaBottomInset - PowerUpAreaTopInset}
}

// ValidJokerSpot keeps a token away from the spawn points, live tanks and
// the current bounds of cover (moved crates and wrecks included).
func ValidJokerSpot(point Vector, starts, tanks []Vector, cover []Rect) bool {
	area := JokerArea()
	if !area.contains(point) {
		return false
	}
	startClearance := math.Max(PowerUpStartClearance, area.Height*PowerUpStartClearanceRate)
	for _, start := range starts {
		if start.distance(point) < startClearance {
			return false
		}
	}
	for _, tank := range tanks {
		if tank.distance(point) < PowerUpTankClearance {
			return false
		}
	}
	for _, bounds := range cover {
		if bounds.inset(-PowerUpObstacleClearance, -PowerUpObstacleClearance).contains(point) {
			return false
		}
	}
	return true
}

// StartJokers deals jokers in this world from the match's generator and bag;
// the first one is due 7–11 s after now.
func (world *World) StartJokers(random Random, bag JokerBag) {
	world.Jokers = &JokerSchedule{Random: random, Bag: bag}
	world.Jokers.NextTokenTick = world.Tick + world.Jokers.Random.spawnInterval()
}

// coverBounds is everything a token keeps clear of: standing furniture and
// crates where they are now, and wrecks.
func (world *World) coverBounds() []Rect {
	cover := make([]Rect, 0, len(world.Obstacles)+len(world.Tanks))
	for _, obstacle := range world.Obstacles {
		if obstacle.Destroyed {
			continue
		}
		authored := world.definition.Obstacles[obstacle.Index]
		width, height := authored.size(world.scale())
		cover = append(cover, rotatedBounds(obstacle.Position, width, height, authored.Rotation))
	}
	radius := world.bodyRadius()
	for _, tank := range world.Tanks {
		if !tank.Active {
			cover = append(cover, Rect{tank.Position.X - radius, tank.Position.Y - radius, 2 * radius, 2 * radius})
		}
	}
	return cover
}

// dealJoker places the joker that is due: up to 80 sampled spots, else a
// retry one second later rather than an unfair spot.
func (world *World) dealJoker() {
	jokers := world.Jokers
	if jokers == nil || world.Tick < jokers.NextTokenTick {
		return
	}
	starts := make([]Vector, len(world.definition.SpawnPoints))
	for index, spawn := range world.definition.SpawnPoints {
		starts[index] = spawn.arena()
	}
	var tanks []Vector
	for _, tank := range world.Tanks {
		if tank.Active {
			tanks = append(tanks, tank.Position)
		}
	}
	cover := world.coverBounds()
	area := JokerArea()
	for range PowerUpSpawnAttempts {
		point := Vector{jokers.Random.Between(area.minX(), area.maxX()), jokers.Random.Between(area.minY(), area.maxY())}
		if !ValidJokerSpot(point, starts, tanks, cover) {
			continue
		}
		kind := jokers.Bag.Next(&jokers.Random)
		world.Token = &Token{Kind: kind, Position: point, ExpiresAtTick: world.Tick + PowerUpTokenLifetimeTicks}
		jokers.NextTokenTick = world.Tick + jokers.Random.spawnInterval()
		return
	}
	jokers.NextTokenTick = world.Tick + PowerUpRetryTicks
}
