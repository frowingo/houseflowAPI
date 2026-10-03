package houseRockets

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	SimulationStateVersion       = 1
	MaximumAdvanceTicks          = PhysicsRateHz / 4
	MaximumMatchTicks      int64 = int64(MaximumMatchDuration/time.Second) * PhysicsRateHz
	maximumCourseEntries         = int(SpawnX+BaseSpeed*BoostMultiplier*float64(MaximumMatchDuration/time.Second)+ViewportWidth+courseLookahead-FirstGateX)/int(GateSpacing) + 2
)

var (
	ErrInvalidSimulation      = errors.New("invalid House Rockets simulation parameters")
	ErrInvalidSimulationState = errors.New("invalid House Rockets simulation state")
	ErrInvalidAdvance         = errors.New("physics batch must be between zero and thirty ticks")
	ErrSimulationFinished     = errors.New("House Rockets simulation is terminal")
	ErrSimulationNotFinished  = errors.New("House Rockets simulation has no result")
	ErrPlayerNotFound         = errors.New("House Rockets player was not found")
	ErrPlayerEliminated       = errors.New("House Rockets player was eliminated")
)

type PlayerIdentity struct {
	PlayerID    string
	DisplayName string
}

type NewSimulationParams struct {
	SessionID string
	HouseID   string
	StartedAt time.Time
	Players   []PlayerIdentity
}

type rocket struct {
	state         PlayerState
	touchedFields map[int]struct{}
}

type courseGate struct {
	gate     Gate
	polygons []Polygon
}

// Simulation is owned by one runtime loop. It performs no I/O and is not safe for concurrent mutation.
type Simulation struct {
	sessionID     string
	houseID       string
	startedAt     time.Time
	tick          int64
	cameraX       float64
	nextGateIndex int
	players       []rocket
	gates         []courseGate
	speedFields   []SpeedField
	outcome       *SimulationOutcome
}

func NewSimulation(params NewSimulationParams) (*Simulation, error) {
	if !validID(params.SessionID) || !validID(params.HouseID) || params.StartedAt.IsZero() || len(params.Players) < MinimumPlayers || len(params.Players) > MaximumPlayers {
		return nil, ErrInvalidSimulation
	}
	seen := make(map[string]bool, len(params.Players))
	for _, player := range params.Players {
		if !validID(player.PlayerID) || strings.TrimSpace(player.DisplayName) == "" || seen[player.PlayerID] {
			return nil, ErrInvalidSimulation
		}
		seen[player.PlayerID] = true
	}
	simulation := &Simulation{sessionID: params.SessionID, houseID: params.HouseID, startedAt: params.StartedAt.UTC(), players: make([]rocket, len(params.Players))}
	colors := PlayerColors()
	for index, player := range params.Players {
		simulation.players[index] = rocket{state: PlayerState{PlayerID: player.PlayerID, DisplayName: player.DisplayName, Color: colors[index],
			WorldX: SpawnX, WorldY: 105 + float64(index)*150/float64(len(params.Players)-1), IsAlive: true}, touchedFields: make(map[int]struct{})}
	}
	simulation.extendCourse()
	return simulation, nil
}

func (simulation *Simulation) Steer(playerID string, courseHeading float64) error {
	if !finite(courseHeading) {
		return ErrInvalidInput
	}
	player := simulation.player(playerID)
	if player == nil {
		return ErrPlayerNotFound
	}
	if !player.state.IsAlive {
		return ErrPlayerEliminated
	}
	if simulation.outcome != nil {
		return ErrSimulationFinished
	}
	player.state.CourseHeading = math.Atan2(math.Sin(courseHeading), math.Cos(courseHeading))
	return nil
}

// AdvanceTicks never substitutes a partial/large delta for the fixed 1/120 second step.
func (simulation *Simulation) AdvanceTicks(ticks int) error {
	if ticks < 0 || ticks > MaximumAdvanceTicks {
		return ErrInvalidAdvance
	}
	if ticks == 0 {
		return nil
	}
	if simulation.outcome != nil {
		return ErrSimulationFinished
	}
	for range ticks {
		simulation.step()
		if simulation.outcome != nil {
			break
		}
	}
	return nil
}

func (simulation *Simulation) step() {
	const stepSeconds = 1.0 / PhysicsRateHz
	simulation.extendCourse()
	for index := range simulation.players {
		player := &simulation.players[index]
		if !player.state.IsAlive {
			continue
		}
		player.state.EffectRemainingSeconds = math.Max(0, player.state.EffectRemainingSeconds-stepSeconds)
		if player.state.EffectRemainingSeconds == 0 {
			player.state.SpeedEffect = nil
		}
		multiplier := 1.0
		if player.state.SpeedEffect != nil {
			multiplier, _ = effectSettings(*player.state.SpeedEffect)
		}
		point := Point{player.state.WorldX + math.Cos(player.state.CourseHeading)*BaseSpeed*multiplier*stepSeconds,
			player.state.WorldY + math.Sin(player.state.CourseHeading)*BaseSpeed*multiplier*stepSeconds}
		for range 3 {
			point.Y = math.Max(RocketRadius, math.Min(TrackHeight-RocketRadius, point.Y))
			for _, gate := range simulation.gates {
				for _, polygon := range gate.polygons {
					point = resolveContact(point, RocketRadius, polygon)
				}
			}
		}
		player.state.WorldX, player.state.WorldY = point.X, point.Y
		for _, field := range simulation.speedFields {
			fieldIndex := fieldIndexFromID(field.ID)
			if _, touched := player.touchedFields[fieldIndex]; touched {
				continue
			}
			if math.Hypot(point.X-field.WorldX, point.Y-field.WorldYAt(float64(simulation.tick+1)/PhysicsRateHz)) > RocketRadius+SpeedFieldRadius {
				continue
			}
			player.touchedFields[fieldIndex] = struct{}{}
			effect := field.Effect
			player.state.SpeedEffect = &effect
			_, player.state.EffectRemainingSeconds = effectSettings(effect)
		}
	}
	leaderX := math.Inf(-1)
	for _, player := range simulation.players {
		if player.state.IsAlive {
			leaderX = math.Max(leaderX, player.state.WorldX)
		}
	}
	simulation.cameraX = math.Max(simulation.cameraX, leaderX-LeaderAnchorX)
	simulation.tick++
	for index := range simulation.players {
		player := &simulation.players[index]
		if player.state.IsAlive && IsBehindCamera(player.state.WorldX, simulation.cameraX) {
			simulation.eliminate(player, EliminationBehindCamera)
		}
	}
	simulation.pruneCourse()
	simulation.resolveOutcome()
	if simulation.outcome == nil && simulation.tick == MaximumMatchTicks {
		simulation.outcome = &SimulationOutcome{Status: ResultCancelled, EndReason: EndSessionExpired}
	}
}

// EliminatePlayers applies a system transition as one batch, resolving ties after the whole batch.
func (simulation *Simulation) EliminatePlayers(playerIDs []string, reason EliminationReason) error {
	if len(playerIDs) == 0 || len(playerIDs) > len(simulation.players) ||
		(reason != EliminationForfeit && reason != EliminationConnectionExpired && reason != EliminationMembershipRevoked) {
		return ErrInvalidInput
	}
	seen := make(map[string]bool, len(playerIDs))
	for _, id := range playerIDs {
		player := simulation.player(id)
		if player == nil {
			return ErrPlayerNotFound
		}
		if seen[id] {
			return ErrInvalidInput
		}
		seen[id] = true
		if simulation.outcome != nil && player.state.IsAlive {
			return ErrSimulationFinished
		}
	}
	for _, id := range playerIDs {
		player := simulation.player(id)
		if player.state.IsAlive {
			simulation.eliminate(player, reason)
		}
	}
	simulation.resolveOutcome()
	return nil
}

func (simulation *Simulation) Cancel(reason EndReason) error {
	if !cancellationReason(reason) || (reason == EndSessionExpired && simulation.tick != MaximumMatchTicks) {
		return ErrInvalidInput
	}
	if simulation.outcome != nil {
		if simulation.outcome.Status == ResultCancelled && simulation.outcome.EndReason == reason {
			return nil
		}
		return ErrSimulationFinished
	}
	simulation.outcome = &SimulationOutcome{Status: ResultCancelled, EndReason: reason}
	return nil
}

func (simulation *Simulation) player(id string) *rocket {
	for index := range simulation.players {
		if simulation.players[index].state.PlayerID == id {
			return &simulation.players[index]
		}
	}
	return nil
}

func (simulation *Simulation) eliminate(player *rocket, reason EliminationReason) {
	tick := simulation.tick
	player.state.IsAlive = false
	player.state.EliminatedAtTick, player.state.EliminationReason = &tick, &reason
}

func (simulation *Simulation) resolveOutcome() {
	if simulation.outcome != nil {
		return
	}
	aliveCount := 0
	var survivor *rocket
	for index := range simulation.players {
		if simulation.players[index].state.IsAlive {
			aliveCount++
			survivor = &simulation.players[index]
		}
	}
	if aliveCount == 1 {
		winnerID := survivor.state.PlayerID
		simulation.outcome = &SimulationOutcome{Status: ResultCompleted, EndReason: EndLastSurvivor, WinnerID: &winnerID}
	} else if aliveCount == 0 {
		simulation.outcome = &SimulationOutcome{Status: ResultCompleted, EndReason: EndSimultaneousElimination}
	}
}

func (simulation *Simulation) extendCourse() {
	for FirstGateX+float64(simulation.nextGateIndex)*GateSpacing < simulation.cameraX+ViewportWidth+courseLookahead {
		simulation.addCourseEntry(simulation.nextGateIndex)
		simulation.nextGateIndex++
	}
}

func (simulation *Simulation) addCourseEntry(index int) {
	gate, _ := GateAt(index)
	if gate.maxX() >= simulation.cameraX-courseTrailingMargin {
		simulation.gates = append(simulation.gates, courseGate{gate, gate.SolidPolygons()})
	}
	if index%5 != 4 {
		field, _ := SpeedFieldAt(index)
		if field.WorldX+SpeedFieldRadius >= simulation.cameraX-courseTrailingMargin {
			simulation.speedFields = append(simulation.speedFields, field)
		}
	}
}

func (simulation *Simulation) pruneCourse() {
	gates := simulation.gates[:0]
	for _, gate := range simulation.gates {
		if gate.gate.maxX() >= simulation.cameraX-courseTrailingMargin {
			gates = append(gates, gate)
		}
	}
	clear(simulation.gates[len(gates):])
	simulation.gates = gates
	fields := simulation.speedFields[:0]
	for _, field := range simulation.speedFields {
		if field.WorldX+SpeedFieldRadius >= simulation.cameraX-courseTrailingMargin {
			fields = append(fields, field)
		}
	}
	simulation.speedFields = fields
	for index := range simulation.players {
		for fieldIndex := range simulation.players[index].touchedFields {
			if FirstGateX+float64(fieldIndex)*GateSpacing+GateSpacing/2+SpeedFieldRadius < simulation.cameraX-courseTrailingMargin {
				delete(simulation.players[index].touchedFields, fieldIndex)
			}
		}
	}
}

func effectSettings(effect SpeedEffect) (multiplier, duration float64) {
	if effect == EffectBoost {
		return BoostMultiplier, BoostDurationSeconds
	}
	if effect == EffectSlow {
		return SlowMultiplier, SlowDurationSeconds
	}
	return 1, 0
}

func cancellationReason(reason EndReason) bool {
	switch reason {
	case EndInsufficientPlayers, EndCancelledByUser, EndSessionExpired, EndRecoveryFailed, EndCoordinationUnavailable, EndRuntimeOverloaded:
		return true
	default:
		return false
	}
}

func validID(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value
}

func fieldIndexFromID(id string) int {
	index, _ := strconv.Atoi(strings.TrimPrefix(id, "field:"))
	return index
}

func (player rocket) stateCopy() PlayerState {
	state := player.state
	state.SpeedEffect = cloneValue(state.SpeedEffect)
	state.EliminatedAtTick = cloneValue(state.EliminatedAtTick)
	state.EliminationReason = cloneValue(state.EliminationReason)
	indices := make([]int, 0, len(player.touchedFields))
	for index := range player.touchedFields {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	state.TouchedFieldIDs = make([]string, 0, len(indices))
	for _, index := range indices {
		state.TouchedFieldIDs = append(state.TouchedFieldIDs, "field:"+strconv.Itoa(index))
	}
	return state
}

func cloneValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
