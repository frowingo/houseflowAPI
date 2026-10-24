package houseTanks

import (
	"errors"
	"math"
)

// The arena simulation is a port of HouseTanksWorld.swift with whole-tick
// timers. A round runs one World; Step advances it by one 1/120 s tick. The
// state is exported so a checkpoint can store and restore it as JSON.

const stepSeconds = 1.0 / PhysicsRateHz

var ErrInvalidWorld = errors.New("house tanks world is invalid")

type PowerUpEffect struct {
	Kind          PowerUpKind `json:"kind"`
	ExpiresAtTick int64       `json:"expiresAtTick"`
}

// Tank is one seat's body. Active is false once destroyed: the tank stays on
// the floor as a wreck that blocks tanks, crates and shots. A tank that is not
// Controllable is a stationary target: it neither turns, drives nor fires.
type Tank struct {
	PlayerID           string         `json:"playerId"`
	Seat               int            `json:"seat"`
	Position           Vector         `json:"position"`
	Velocity           Vector         `json:"velocity"`
	Rotation           float64        `json:"rotation"`
	RotationDirection  float64        `json:"rotationDirection"`
	Active             bool           `json:"active"`
	Controllable       bool           `json:"controllable"`
	Driving            bool           `json:"driving"`
	DriveDirection     Vector         `json:"driveDirection"`
	HasFired           bool           `json:"hasFired"`
	LastFireTick       int64          `json:"lastFireTick"`
	AimLocks           int            `json:"aimLocks"`
	Armor              int            `json:"armor"`
	ProtectedUntilTick int64          `json:"protectedUntilTick"`
	PowerUp            *PowerUpEffect `json:"powerUp"`
}

// Obstacle is the moving part of a map obstacle; its shape comes from the map.
type Obstacle struct {
	Index     int    `json:"index"`
	Position  Vector `json:"position"`
	Velocity  Vector `json:"velocity"`
	Destroyed bool   `json:"destroyed"`
}

type Projectile struct {
	ID        int64   `json:"id"`
	OwnerID   string  `json:"ownerId"`
	Position  Vector  `json:"position"`
	Velocity  Vector  `json:"velocity"`
	Rocket    bool    `json:"rocket"`
	HoldsAim  bool    `json:"holdsAim"`
	Radius    float64 `json:"radius"`
	Age       int64   `json:"age"`
	SpawnTick int64   `json:"spawnTick"`
}

func (projectile Projectile) lifetime() int64 {
	if projectile.Rocket {
		return RocketLifetimeTicks
	}
	return ShellLifetimeTicks
}

// Token is the joker lying on the floor; at most one exists.
type Token struct {
	Kind          PowerUpKind `json:"kind"`
	Position      Vector      `json:"position"`
	ExpiresAtTick int64       `json:"expiresAtTick"`
}

// WorldEvent is one effect for renderers. The runtime numbers them.
type WorldEvent struct {
	Tick       int64       `json:"tick"`
	Kind       EventKind   `json:"kind"`
	PlayerID   string      `json:"playerId,omitempty"`
	SourceID   string      `json:"sourceId,omitempty"`
	Position   *Vector     `json:"position,omitempty"`
	Angle      *float64    `json:"angle,omitempty"`
	Armor      *int        `json:"armor,omitempty"`
	Lethal     bool        `json:"lethal,omitempty"`
	CrateIndex *int        `json:"crateIndex,omitempty"`
	PowerUp    PowerUpKind `json:"powerUp,omitempty"`
}

// Hit is a shot that reached an unshielded live tank; it is applied at the
// end of the tick, so tanks destroyed in the same tick end a round as a draw.
type Hit struct {
	TargetID string `json:"targetId"`
	OwnerID  string `json:"ownerId"`
	Lethal   bool   `json:"lethal"`
}

type World struct {
	MapID            MapID        `json:"mapId"`
	MapVersion       int          `json:"mapVersion"`
	Tick             int64        `json:"tick"`
	Tanks            []Tank       `json:"tanks"`
	Obstacles        []Obstacle   `json:"obstacles"`
	Projectiles      []Projectile `json:"projectiles"`
	Token            *Token       `json:"token"`
	NextProjectileID int64        `json:"nextProjectileId"`
	// Jokers deals tokens while the round plays; nil deals none.
	Jokers *JokerSchedule `json:"jokers"`
	// PendingHits are muzzle hits of a press, applied by the next step.
	PendingHits []Hit `json:"pendingHits"`
	definition  MapDefinition
	events      []WorldEvent
}

// NewWorld seats the players in order on the map's spawn points, every tank
// facing the middle of the arena; neighbours sweep in opposite directions.
func NewWorld(definition MapDefinition, playerIDs []string, tick int64) (*World, error) {
	if len(playerIDs) == 0 || len(playerIDs) > len(definition.SpawnPoints) || tick < 0 {
		return nil, ErrInvalidWorld
	}
	world := &World{MapID: definition.ID, MapVersion: definition.Version, Tick: tick, NextProjectileID: 1, definition: definition,
		Tanks: make([]Tank, 0, len(playerIDs)), Obstacles: make([]Obstacle, 0, len(definition.Obstacles)), Projectiles: []Projectile{}, PendingHits: []Hit{}}
	center := Vector{ArenaWidth / 2, ArenaHeight / 2}
	for seat, playerID := range playerIDs {
		if !validID(playerID) || world.tankIndex(playerID) >= 0 {
			return nil, ErrInvalidWorld
		}
		spawn := definition.SpawnPoints[seat].arena()
		direction := 1.0
		if seat%2 == 1 {
			direction = -1
		}
		world.Tanks = append(world.Tanks, Tank{PlayerID: playerID, Seat: seat, Position: spawn, Rotation: center.sub(spawn).angle(),
			RotationDirection: direction, Active: true, Controllable: true, Armor: ArmorPerRound})
	}
	for index, obstacle := range definition.Obstacles {
		world.Obstacles = append(world.Obstacles, Obstacle{Index: index, Position: obstacle.center()})
	}
	return world, nil
}

func validID(value string) bool { return value != "" && len(value) <= 128 }

func (world *World) Definition() MapDefinition { return world.definition }

func (world *World) scale() float64      { return world.definition.Scale }
func (world *World) bodyRadius() float64 { return TankRadius * world.scale() }

func (world *World) tankIndex(playerID string) int {
	for index := range world.Tanks {
		if world.Tanks[index].PlayerID == playerID {
			return index
		}
	}
	return -1
}

// TankOf returns a copy of the player's tank.
func (world *World) TankOf(playerID string) (Tank, bool) {
	if index := world.tankIndex(playerID); index >= 0 {
		return world.Tanks[index], true
	}
	return Tank{}, false
}

func (world *World) powerUp(index int) PowerUpKind {
	if effect := world.Tanks[index].PowerUp; effect != nil && effect.ExpiresAtTick > world.Tick {
		return effect.Kind
	}
	return ""
}

func (world *World) emit(event WorldEvent) {
	event.Tick = world.Tick
	world.events = append(world.events, event)
}

// DrainEvents returns and forgets the events since the last call.
func (world *World) DrainEvents() []WorldEvent {
	events := world.events
	world.events = nil
	return events
}

// Press locks the heading, fires once and starts driving along the barrel. A
// shot still cooling down is skipped but the tank drives anyway. It reports
// whether a shot was fired.
func (world *World) Press(playerID string) bool {
	index := world.tankIndex(playerID)
	if index < 0 || !world.Tanks[index].Active || !world.Tanks[index].Controllable || world.Tanks[index].Driving {
		return false
	}
	tank := &world.Tanks[index]
	tank.Driving = true
	tank.DriveDirection = vectorFromAngle(tank.Rotation)
	hits, fired := world.fire(index)
	world.PendingHits = append(world.PendingHits, hits...)
	return fired
}

// Release stops the tank; it sweeps the other way afterwards.
func (world *World) Release(playerID string) {
	index := world.tankIndex(playerID)
	if index < 0 || !world.Tanks[index].Driving {
		return
	}
	tank := &world.Tanks[index]
	tank.Driving, tank.DriveDirection, tank.Velocity = false, Vector{}, Vector{}
	if tank.RotationDirection >= 0 {
		tank.RotationDirection = -1
	} else {
		tank.RotationDirection = 1
	}
}

// SetControllable turns a tank into a stationary target or back. Losing
// control releases the press without reversing the sweep, stops the tank and
// clears its aim lock; the body, armor and joker stay.
func (world *World) SetControllable(playerID string, controllable bool) {
	index := world.tankIndex(playerID)
	if index < 0 || world.Tanks[index].Controllable == controllable {
		return
	}
	tank := &world.Tanks[index]
	tank.Controllable = controllable
	if !controllable {
		tank.Driving, tank.DriveDirection, tank.Velocity, tank.AimLocks = false, Vector{}, Vector{}, 0
	}
}

// SetRotation turns an idle tank to a lag-compensated heading.
func (world *World) SetRotation(playerID string, rotation float64) bool {
	index := world.tankIndex(playerID)
	if index < 0 || !world.Tanks[index].Active || world.Tanks[index].Driving || math.IsNaN(rotation) || math.IsInf(rotation, 0) {
		return false
	}
	world.Tanks[index].Rotation = rotation
	return true
}

// Grant gives a live tank a joker effect from now, replacing any other.
func (world *World) Grant(playerID string, kind PowerUpKind) bool {
	index := world.tankIndex(playerID)
	if index < 0 || !world.Tanks[index].Active || !kind.valid() {
		return false
	}
	world.Tanks[index].PowerUp = &PowerUpEffect{Kind: kind, ExpiresAtTick: world.Tick + PowerUpDurationTicks}
	return true
}

// Freeze stops the arena at a round's end: no shots, motion, presses, aim
// locks or jokers. A frozen world is shown but not stepped again.
func (world *World) Freeze() {
	world.Projectiles, world.PendingHits, world.Token, world.Jokers = []Projectile{}, []Hit{}, nil, nil
	for index := range world.Tanks {
		tank := &world.Tanks[index]
		tank.Velocity, tank.Driving, tank.AimLocks, tank.PowerUp = Vector{}, false, 0, nil
	}
	for index := range world.Obstacles {
		world.Obstacles[index].Velocity = Vector{}
	}
}

// PlaceToken lays a joker on the floor unless one is already there.
func (world *World) PlaceToken(kind PowerUpKind, position Vector) bool {
	if world.Token != nil || !kind.valid() || !ArenaRect().contains(position) {
		return false
	}
	world.Token = &Token{Kind: kind, Position: position, ExpiresAtTick: world.Tick + PowerUpTokenLifetimeTicks}
	return true
}

// Step advances the arena by one tick in the reference order: jokers, tank
// motion, crate motion, three contact passes, joker pickup, shots, hits.
func (world *World) Step() {
	world.Tick++
	world.expirePowerUps()
	world.dealJoker()
	var hits []Hit
	for index := range world.Tanks {
		tank := &world.Tanks[index]
		if !tank.Active {
			continue
		}
		if tank.Driving {
			tank.Velocity = tank.Velocity.add(tank.DriveDirection.scale(DriveForce / TankMass * stepSeconds))
			if world.powerUp(index) == PowerUpRapidFire {
				fired, _ := world.fire(index)
				hits = append(hits, fired...)
			}
		} else if tank.AimLocks == 0 && tank.Controllable {
			tank.Rotation += RotationSpeed * tank.RotationDirection * stepSeconds
		}
		tank.Velocity = tank.Velocity.scale(1 / (1 + stepSeconds*TankDamping))
		if speed := tank.Velocity.length(); speed > MaximumTankSpeed {
			tank.Velocity = tank.Velocity.scale(MaximumTankSpeed / speed)
		}
		tank.Position = tank.Position.add(tank.Velocity.scale(stepSeconds))
	}
	for index := range world.Obstacles {
		obstacle := &world.Obstacles[index]
		if world.definition.Obstacles[index].Style != StyleCrate || obstacle.Destroyed {
			continue
		}
		obstacle.Velocity = obstacle.Velocity.scale(1 / (1 + stepSeconds*CrateDamping))
		obstacle.Position = obstacle.Position.add(obstacle.Velocity.scale(stepSeconds))
	}
	for range ContactIterations {
		world.resolveContacts()
	}
	world.collectToken()
	hits = append(hits, world.advanceProjectiles()...)
	world.applyHits(hits)
}

func (world *World) expirePowerUps() {
	if world.Token != nil && world.Tick >= world.Token.ExpiresAtTick {
		world.Token = nil
	}
	for index := range world.Tanks {
		if effect := world.Tanks[index].PowerUp; effect != nil && world.Tick >= effect.ExpiresAtTick {
			world.Tanks[index].PowerUp = nil
		}
	}
}

// fire launches one shot, or a spread-shot volley, unless the barrel is still
// cooling down. A muzzle already touching cover or a tank hits it at once.
func (world *World) fire(index int) ([]Hit, bool) {
	tank := world.Tanks[index]
	power := world.powerUp(index)
	cooldown := int64(FireCooldownTicks)
	if power == PowerUpRapidFire {
		cooldown = RapidFireCooldownTicks
	}
	if tank.HasFired && world.Tick-tank.LastFireTick < cooldown {
		return nil, false
	}
	world.Tanks[index].HasFired, world.Tanks[index].LastFireTick = true, world.Tick
	rocket := power == PowerUpRocket
	offsets := []float64{0}
	if power == PowerUpSpreadShot {
		offsets = SpreadAngles()
	}
	var hits []Hit
	for _, offset := range offsets {
		direction := vectorFromAngle(tank.Rotation + offset)
		muzzle, speed, radius := ShellMuzzleOffset, ShellSpeed, ShellRadius
		if rocket {
			muzzle, speed, radius = RocketMuzzleOffset, RocketSpeed, RocketRadius
		}
		start := tank.Position.add(direction.scale(muzzle * world.scale()))
		projectile := Projectile{ID: world.NextProjectileID, OwnerID: tank.PlayerID, Position: start, Velocity: direction.scale(speed),
			Rocket: rocket, HoldsAim: power == PowerUpAim, Radius: radius * world.scale(), SpawnTick: world.Tick}
		world.NextProjectileID++
		if projectile.HoldsAim {
			world.Tanks[index].AimLocks++
		}
		// The muzzle may touch a pushed crate or a thin wall; never spawn a
		// shot on the far side of that cover.
		if contact, found := world.firstContact(tank.Position, start, projectile.Radius, tank.PlayerID); found {
			hits = append(hits, world.impact(projectile, contact, start)...)
		} else {
			world.Projectiles = append(world.Projectiles, projectile)
		}
	}
	angle := tank.Rotation
	world.emit(WorldEvent{Kind: EventFired, PlayerID: tank.PlayerID, Angle: &angle})
	return hits, true
}

type bodyKind int

const (
	bodyWall bodyKind = iota
	bodyTank
	bodyObstacle
)

type contact struct {
	kind     bodyKind
	index    int
	fraction float64
}

// firstContact is the first wall, tank (live or wreck, never the owner) or
// standing obstacle the swept shot touches.
func (world *World) firstContact(start, end Vector, radius float64, ownerID string) (contact, bool) {
	best, found := contact{fraction: math.Inf(1)}, false
	consider := func(kind bodyKind, index int, fraction float64, hit bool) {
		if hit && fraction >= 0 && fraction <= 1 && fraction < best.fraction {
			best, found = contact{kind, index, fraction}, true
		}
	}
	fraction, hit := exitFraction(start, end, ArenaFrame().inset(radius, radius))
	consider(bodyWall, 0, fraction, hit)
	for index, tank := range world.Tanks {
		if tank.PlayerID == ownerID {
			continue
		}
		fraction, hit := segmentCircle(start, end, tank.Position, world.bodyRadius()+radius)
		consider(bodyTank, index, fraction, hit)
	}
	for index, obstacle := range world.Obstacles {
		if obstacle.Destroyed {
			continue
		}
		definition := world.definition.Obstacles[index]
		width, height := definition.size(world.scale())
		var fraction float64
		var hit bool
		if definition.Style.round() {
			fraction, hit = segmentCircle(start, end, obstacle.Position, math.Min(width, height)/2+radius)
		} else {
			fraction, hit = segmentBox(start, end, obstacle.Position, width/2+radius, height/2+radius, definition.Rotation)
		}
		consider(bodyObstacle, index, fraction, hit)
	}
	return best, found
}

func (world *World) advanceProjectiles() []Hit {
	hits := world.PendingHits
	world.PendingHits = []Hit{}
	survivors := world.Projectiles[:0]
	for _, projectile := range world.Projectiles {
		projectile.Age++
		start := projectile.Position
		end := start.add(projectile.Velocity.scale(stepSeconds))
		if contact, found := world.firstContact(start, end, projectile.Radius, projectile.OwnerID); found {
			point := start.add(end.sub(start).scale(contact.fraction))
			hits = append(hits, world.impact(projectile, contact, point)...)
			continue
		}
		projectile.Position = end
		if projectile.Age >= projectile.lifetime() {
			world.releaseAim(projectile)
			continue
		}
		survivors = append(survivors, projectile)
	}
	world.Projectiles = survivors
	return hits
}

// impact ends a shot: a crate breaks, a shield absorbs the shot, a live tank
// is hit. Walls, furniture and wrecks only stop it.
func (world *World) impact(projectile Projectile, body contact, point Vector) []Hit {
	world.releaseAim(projectile)
	if projectile.Rocket {
		world.emit(WorldEvent{Kind: EventExplosion, Position: &point})
	}
	switch body.kind {
	case bodyObstacle:
		if world.definition.Obstacles[body.index].Style != StyleCrate {
			return nil
		}
		world.Obstacles[body.index].Destroyed = true
		position, crate := world.Obstacles[body.index].Position, body.index
		world.emit(WorldEvent{Kind: EventCrateBroken, CrateIndex: &crate, Position: &position})
	case bodyTank:
		target := world.Tanks[body.index]
		if !target.Active {
			return nil
		}
		if world.powerUp(body.index) == PowerUpShield {
			world.emit(WorldEvent{Kind: EventShielded, PlayerID: target.PlayerID, SourceID: projectile.OwnerID})
			return nil
		}
		return []Hit{{TargetID: target.PlayerID, OwnerID: projectile.OwnerID, Lethal: projectile.Rocket}}
	}
	return nil
}

// releaseAim lets the owner turn again once its last aimed shot has landed.
func (world *World) releaseAim(projectile Projectile) {
	if !projectile.HoldsAim {
		return
	}
	if index := world.tankIndex(projectile.OwnerID); index >= 0 && world.Tanks[index].AimLocks > 0 {
		world.Tanks[index].AimLocks--
	}
}

// applyHits takes armor in order. A regular shot right after a hit is
// ignored for HitProtectionTicks; a rocket destroys whatever armor is left.
func (world *World) applyHits(hits []Hit) {
	for _, hit := range hits {
		index := world.tankIndex(hit.TargetID)
		if index < 0 || !world.Tanks[index].Active {
			continue
		}
		tank := &world.Tanks[index]
		if !hit.Lethal && world.Tick < tank.ProtectedUntilTick {
			continue
		}
		tank.ProtectedUntilTick = world.Tick + HitProtectionTicks
		if hit.Lethal {
			tank.Armor = 0
		} else {
			tank.Armor--
		}
		armor := tank.Armor
		world.emit(WorldEvent{Kind: EventHit, PlayerID: tank.PlayerID, SourceID: hit.OwnerID, Armor: &armor, Lethal: hit.Lethal})
		if tank.Armor == 0 {
			world.eliminate(index, hit.OwnerID)
		}
	}
}

func (world *World) eliminate(index int, sourceID string) {
	tank := &world.Tanks[index]
	tank.Active, tank.Velocity, tank.Driving, tank.DriveDirection, tank.AimLocks, tank.PowerUp = false, Vector{}, false, Vector{}, 0, nil
	position := tank.Position
	world.emit(WorldEvent{Kind: EventEliminated, PlayerID: tank.PlayerID, SourceID: sourceID, Position: &position})
}

// collectToken gives the floor joker to the first live tank over it.
func (world *World) collectToken() {
	token := world.Token
	if token == nil {
		return
	}
	reach := (PowerUpTokenRadius + TankRadius) * world.scale()
	for index, tank := range world.Tanks {
		if !tank.Active || tank.Position.distance(token.Position) >= reach {
			continue
		}
		world.Token = nil
		world.Tanks[index].PowerUp = &PowerUpEffect{Kind: token.Kind, ExpiresAtTick: world.Tick + PowerUpDurationTicks}
		world.emit(WorldEvent{Kind: EventJokerCollected, PlayerID: tank.PlayerID, PowerUp: token.Kind})
		return
	}
}

func (world *World) obstacleShape(index int) (definition MapObstacle, width, height float64) {
	definition = world.definition.Obstacles[index]
	width, height = definition.size(world.scale())
	return definition, width, height
}

func (world *World) obstacleMovable(index int) bool {
	return world.definition.Obstacles[index].Style == StyleCrate
}

// circleContact is the push-out direction and depth of a circle overlapping
// an obstacle in its current position.
func (world *World) circleContact(center Vector, radius float64, index int) (Vector, float64, bool) {
	definition, width, height := world.obstacleShape(index)
	position := world.Obstacles[index].Position
	if definition.Style.round() {
		offset := center.sub(position)
		distance := offset.length()
		minimum := radius + math.Min(width, height)/2
		if distance >= minimum {
			return Vector{}, 0, false
		}
		normal := Vector{1, 0}
		if distance > 0.0001 {
			normal = offset.scale(1 / distance)
		}
		return normal, minimum - distance, true
	}
	local := center.sub(position).rotated(-definition.Rotation)
	halfWidth, halfHeight := width/2, height/2
	closest := Vector{math.Min(math.Max(local.X, -halfWidth), halfWidth), math.Min(math.Max(local.Y, -halfHeight), halfHeight)}
	offset := local.sub(closest)
	if distance := offset.length(); distance > 0.0001 {
		if distance >= radius {
			return Vector{}, 0, false
		}
		return offset.scale(1 / distance).rotated(definition.Rotation), radius - distance, true
	}
	// The center is inside the box: leave through the nearest face.
	pushX, pushY := halfWidth-math.Abs(local.X), halfHeight-math.Abs(local.Y)
	normal := Vector{0, sign(local.Y)}
	if pushX < pushY {
		normal = Vector{sign(local.X), 0}
	}
	return normal.rotated(definition.Rotation), math.Min(pushX, pushY) + radius, true
}

func sign(value float64) float64 {
	if value >= 0 {
		return 1
	}
	return -1
}

func (world *World) resolveContacts() {
	frame := ArenaFrame()
	radius := world.bodyRadius()
	// Tanks against the frame, furniture, wrecks, crates and each other.
	for a := range world.Tanks {
		if !world.Tanks[a].Active {
			continue
		}
		world.confine(a, frame)
		for o := range world.Obstacles {
			if world.Obstacles[o].Destroyed {
				continue
			}
			normal, depth, touching := world.circleContact(world.Tanks[a].Position, radius, o)
			if !touching {
				continue
			}
			if world.obstacleMovable(o) {
				world.push(a, o, normal, depth)
			} else {
				world.Tanks[a].Position = world.Tanks[a].Position.add(normal.scale(depth))
				world.Tanks[a].Velocity = reflect(world.Tanks[a].Velocity, normal, ObstacleRestitution)
			}
		}
		for b := range world.Tanks {
			if b == a {
				continue
			}
			tankA, tankB := &world.Tanks[a], &world.Tanks[b]
			offset := tankA.Position.sub(tankB.Position)
			distance := offset.length()
			minimum := radius * 2
			if distance >= minimum {
				continue
			}
			normal := Vector{1, 0}
			if distance > 0.0001 {
				normal = offset.scale(1 / distance)
			}
			depth := minimum - distance
			if !tankB.Active {
				tankA.Position = tankA.Position.add(normal.scale(depth))
				tankA.Velocity = reflect(tankA.Velocity, normal, ObstacleRestitution)
				continue
			}
			if b < a {
				continue // each live pair once
			}
			tankA.Position = tankA.Position.add(normal.scale(depth / 2))
			tankB.Position = tankB.Position.sub(normal.scale(depth / 2))
			if relative := tankA.Velocity.sub(tankB.Velocity).dot(normal); relative < 0 {
				impulse := -(1 + TankRestitution) * relative / 2
				tankA.Velocity = tankA.Velocity.add(normal.scale(impulse))
				tankB.Velocity = tankB.Velocity.sub(normal.scale(impulse))
			}
		}
	}
	// Crates against the frame, fixed cover, wrecks and each other.
	for c := range world.Obstacles {
		if !world.obstacleMovable(c) || world.Obstacles[c].Destroyed {
			continue
		}
		definition, width, height := world.obstacleShape(c)
		crate := &world.Obstacles[c]
		bounds := rotatedBounds(crate.Position, width, height, definition.Rotation)
		shift := Vector{}
		if bounds.minX() < frame.minX() {
			shift.X = frame.minX() - bounds.minX()
		}
		if bounds.maxX() > frame.maxX() {
			shift.X = frame.maxX() - bounds.maxX()
		}
		if bounds.minY() < frame.minY() {
			shift.Y = frame.minY() - bounds.minY()
		}
		if bounds.maxY() > frame.maxY() {
			shift.Y = frame.maxY() - bounds.maxY()
		}
		if shift != (Vector{}) {
			crate.Position = crate.Position.add(shift)
			crate.Velocity = reflect(crate.Velocity, shift.normalized(), CrateRestitution)
		}
		crateRadius := math.Min(width, height) / 2 * 1.05
		for o := range world.Obstacles {
			if o == c || world.Obstacles[o].Destroyed {
				continue
			}
			normal, depth, touching := world.circleContact(crate.Position, crateRadius, o)
			if !touching {
				continue
			}
			if world.obstacleMovable(o) {
				if o < c {
					continue
				}
				crate.Position = crate.Position.add(normal.scale(depth / 2))
				world.Obstacles[o].Position = world.Obstacles[o].Position.sub(normal.scale(depth / 2))
			} else {
				crate.Position = crate.Position.add(normal.scale(depth))
				crate.Velocity = reflect(crate.Velocity, normal, CrateRestitution)
			}
		}
		for _, tank := range world.Tanks {
			if tank.Active {
				continue
			}
			offset := crate.Position.sub(tank.Position)
			distance := offset.length()
			minimum := crateRadius + radius
			if distance < minimum && distance > 0.0001 {
				crate.Position = crate.Position.add(offset.scale((minimum - distance) / distance))
			}
		}
	}
}

func (world *World) confine(index int, frame Rect) {
	radius := world.bodyRadius()
	tank := &world.Tanks[index]
	if tank.Position.X < frame.minX()+radius {
		tank.Position.X = frame.minX() + radius
		if tank.Velocity.X < 0 {
			tank.Velocity.X *= -TankRestitution
		}
	}
	if tank.Position.X > frame.maxX()-radius {
		tank.Position.X = frame.maxX() - radius
		if tank.Velocity.X > 0 {
			tank.Velocity.X *= -TankRestitution
		}
	}
	if tank.Position.Y < frame.minY()+radius {
		tank.Position.Y = frame.minY() + radius
		if tank.Velocity.Y < 0 {
			tank.Velocity.Y *= -TankRestitution
		}
	}
	if tank.Position.Y > frame.maxY()-radius {
		tank.Position.Y = frame.maxY() - radius
		if tank.Velocity.Y > 0 {
			tank.Velocity.Y *= -TankRestitution
		}
	}
}

// push splits a tank–crate overlap by inverse mass, so a tank shoves a light
// crate ahead of it.
func (world *World) push(tankIndex, crateIndex int, normal Vector, depth float64) {
	tank, crate := &world.Tanks[tankIndex], &world.Obstacles[crateIndex]
	inverseTank, inverseCrate := 1/TankMass, 1/CrateMass
	total := inverseTank + inverseCrate
	tank.Position = tank.Position.add(normal.scale(depth * inverseTank / total))
	crate.Position = crate.Position.sub(normal.scale(depth * inverseCrate / total))
	relative := tank.Velocity.sub(crate.Velocity).dot(normal)
	if relative >= 0 {
		return
	}
	impulse := -(1 + CrateRestitution) * relative / total
	tank.Velocity = tank.Velocity.add(normal.scale(impulse * inverseTank))
	crate.Velocity = crate.Velocity.sub(normal.scale(impulse * inverseCrate))
}
