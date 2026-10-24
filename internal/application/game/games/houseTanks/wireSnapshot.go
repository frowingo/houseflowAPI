package houseTanks

import "time"

// FramePlayer is a seat in a frame. IsAlive is what the transport reads:
// the player keeps control rights in the match (has not left), whatever
// happened to the tank this round.
type FramePlayer struct {
	PlayerID             string       `json:"playerId"`
	DisplayName          string       `json:"displayName"`
	Seat                 int          `json:"seat"`
	IsAlive              bool         `json:"isAlive"`
	HasLeft              bool         `json:"hasLeft"`
	TankAlive            bool         `json:"tankAlive"`
	Armor                int          `json:"armor"`
	RoundsWon            int          `json:"roundsWon"`
	RoundReady           bool         `json:"roundReady"`
	PowerUp              *PowerUpKind `json:"powerUp"`
	PowerUpExpiresAtTick *int64       `json:"powerUpExpiresAtTick"`
}

// FrameWorld is the public match state of a frame, already in wire shapes.
// The proposed champion stays out of it until the result commits.
type FrameWorld struct {
	SessionID              string                      `json:"sessionId"`
	Tick                   int64                       `json:"tick"`
	MapID                  MapID                       `json:"mapId"`
	MapVersion             int                         `json:"mapVersion"`
	Seed                   string                      `json:"seed"`
	RoundPhase             *RoundPhase                 `json:"roundPhase"`
	RoundNumber            int                         `json:"roundNumber"`
	RoundCountdownEndsAt   *time.Time                  `json:"roundCountdownEndsAt"`
	RoundReadyWindowEndsAt *time.Time                  `json:"roundReadyWindowEndsAt"`
	Players                []FramePlayer               `json:"players"`
	Tanks                  []HouseTanksTankModel       `json:"tanks"`
	Crates                 []HouseTanksCrateModel      `json:"crates"`
	Token                  *HouseTanksTokenModel       `json:"token"`
	Projectiles            []HouseTanksProjectileModel `json:"projectiles"`
	Events                 []HouseTanksEventModel      `json:"events"`
	RoundWinnerID          *string                     `json:"roundWinnerId"`
}

func optional[T comparable](value T) *T {
	var zero T
	if value == zero {
		return nil
	}
	return &value
}

func frameWorld(session RuntimeSession, match *Match) FrameWorld {
	world := match.World
	phase := match.RoundPhase
	frame := FrameWorld{SessionID: session.SessionID, Tick: match.Tick, MapID: match.MapID, MapVersion: match.MapVersion, Seed: SeedText(match.Seed),
		RoundPhase: &phase, RoundNumber: match.RoundNumber, Players: make([]FramePlayer, 0, len(match.Players)), Tanks: make([]HouseTanksTankModel, 0, len(world.Tanks)),
		Crates: []HouseTanksCrateModel{}, Projectiles: make([]HouseTanksProjectileModel, 0, len(world.Projectiles)), Events: make([]HouseTanksEventModel, 0, len(match.Events)),
		RoundWinnerID: optional(match.RoundWinnerID)}
	if phase == RoundCountdown {
		endsAt := session.StartedAt.Add(ticksDuration(match.RoundCountdownEndsAtTick)).UTC()
		frame.RoundCountdownEndsAt = &endsAt
	}
	if match.ReadyWindowEndsAtTick != nil {
		endsAt := session.StartedAt.Add(ticksDuration(*match.ReadyWindowEndsAtTick)).UTC()
		frame.RoundReadyWindowEndsAt = &endsAt
	}
	for index, player := range match.Players {
		tank := world.Tanks[index]
		item := FramePlayer{PlayerID: player.PlayerID, DisplayName: session.Players[index].DisplayName, Seat: player.Seat, IsAlive: !player.HasLeft, HasLeft: player.HasLeft,
			TankAlive: tank.Active, Armor: tank.Armor, RoundsWon: player.RoundsWon, RoundReady: player.RoundReady}
		if effect := tank.PowerUp; effect != nil {
			kind, expiresAt := effect.Kind, effect.ExpiresAtTick
			item.PowerUp, item.PowerUpExpiresAtTick = &kind, &expiresAt
		}
		frame.Players = append(frame.Players, item)
		direction := 1
		if tank.RotationDirection < 0 {
			direction = -1
		}
		frame.Tanks = append(frame.Tanks, HouseTanksTankModel{PlayerID: tank.PlayerID, X: tank.Position.X, Y: tank.Position.Y, VX: tank.Velocity.X, VY: tank.Velocity.Y,
			Rotation: tank.Rotation, RotationDirection: direction, IsDriving: tank.Driving, AimLocks: tank.AimLocks, Controllable: tank.Controllable})
	}
	for _, obstacle := range world.Obstacles {
		if match.definition.Obstacles[obstacle.Index].Style == StyleCrate {
			frame.Crates = append(frame.Crates, HouseTanksCrateModel{Index: obstacle.Index, X: obstacle.Position.X, Y: obstacle.Position.Y, Destroyed: obstacle.Destroyed})
		}
	}
	if token := world.Token; token != nil {
		frame.Token = &HouseTanksTokenModel{Kind: token.Kind, X: token.Position.X, Y: token.Position.Y, ExpiresAtTick: token.ExpiresAtTick}
	}
	for _, projectile := range world.Projectiles {
		frame.Projectiles = append(frame.Projectiles, HouseTanksProjectileModel{ID: projectile.ID, OwnerID: projectile.OwnerID, X: projectile.Position.X, Y: projectile.Position.Y,
			VX: projectile.Velocity.X, VY: projectile.Velocity.Y, Rocket: projectile.Rocket, SpawnTick: projectile.SpawnTick})
	}
	for _, event := range match.Events {
		frame.Events = append(frame.Events, eventModel(event))
	}
	return frame
}

// eventModel keeps only the fields the event's kind documents.
func eventModel(event MatchEvent) HouseTanksEventModel {
	model := HouseTanksEventModel{Sequence: event.Sequence, Tick: event.Tick, Kind: event.Kind}
	var x, y *float64
	if event.Position != nil {
		px, py := event.Position.X, event.Position.Y
		x, y = &px, &py
	}
	switch event.Kind {
	case EventFired:
		model.PlayerID, model.Angle = optional(event.PlayerID), event.Angle
	case EventHit:
		model.PlayerID, model.SourceID, model.Armor, model.Lethal = optional(event.PlayerID), optional(event.SourceID), event.Armor, optional(event.Lethal)
	case EventShielded:
		model.PlayerID, model.SourceID = optional(event.PlayerID), optional(event.SourceID)
	case EventCrateBroken:
		model.CrateIndex, model.X, model.Y = event.CrateIndex, x, y
	case EventExplosion:
		model.X, model.Y = x, y
	case EventEliminated:
		model.PlayerID, model.SourceID, model.X, model.Y = optional(event.PlayerID), optional(event.SourceID), x, y
	case EventJokerCollected:
		model.PlayerID, model.PowerUp = optional(event.PlayerID), optional(event.PowerUp)
	case EventRoundEnded:
		round := event.RoundNumber
		model.RoundNumber, model.WinnerID = &round, optional(event.WinnerID)
	}
	return model
}

// SnapshotForConnection projects a frame for one connection: only the
// viewer's own control generation is shown, and the champion only once the
// result has committed.
func SnapshotForConnection(frame RuntimeFrame, playerID, connectionID string, grant *HouseTanksControlGrantedModel) HouseTanksSnapshotModel {
	world := frame.World
	model := HouseTanksSnapshotModel{SessionID: world.SessionID, GameKey: GameKey, RuntimeEpoch: frame.RuntimeEpoch, StateSequence: frame.StateSequence, Tick: world.Tick,
		Phase: frame.Phase, MapID: world.MapID, MapVersion: world.MapVersion, Seed: world.Seed, RoundPhase: world.RoundPhase, RoundNumber: world.RoundNumber,
		CountdownEndsAt: frame.CountdownEndsAt, RoundCountdownEndsAt: world.RoundCountdownEndsAt, RoundReadyWindowEndsAt: world.RoundReadyWindowEndsAt,
		Players: make([]HouseTanksPlayerModel, 0, len(world.Players)), Tanks: world.Tanks, Crates: world.Crates, Token: world.Token, Projectiles: world.Projectiles,
		Events: world.Events, RoundWinnerID: world.RoundWinnerID}
	if frame.CommittedResult != nil && frame.Phase == PhaseEnded && frame.CommittedResult.ChampionID != nil {
		champion := *frame.CommittedResult.ChampionID
		model.ChampionID = &champion
	}
	colors := PlayerColors()
	for index, player := range world.Players {
		control := RuntimePlayerControl{}
		if index < len(frame.Controls) && frame.Controls[index].PlayerID == player.PlayerID {
			control = frame.Controls[index]
		}
		item := HouseTanksPlayerModel{PlayerID: player.PlayerID, DisplayName: player.DisplayName, Seat: player.Seat, Color: colors[player.Seat%len(colors)], Armor: player.Armor,
			RoundsWon: player.RoundsWon, IsAlive: player.TankAlive, Connected: control.Connected, HasLeft: player.HasLeft, RoundReady: player.RoundReady, PowerUp: player.PowerUp,
			PowerUpExpiresAtTick: player.PowerUpExpiresAtTick, LastProcessedInputSequence: control.LastProcessedInputSequence}
		if player.IsAlive && frame.Phase == PhasePlaying && grant != nil && grant.RuntimeEpoch == frame.RuntimeEpoch && grant.PlayerID == playerID &&
			player.PlayerID == playerID && control.ConnectionID == connectionID && control.Connected {
			generation := grant.ControlGeneration
			item.ControlGeneration = &generation
		}
		model.Players = append(model.Players, item)
	}
	return model
}
