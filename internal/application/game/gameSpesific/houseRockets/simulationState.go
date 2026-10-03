package houseRockets

import (
	"math"
	"strconv"
	"strings"
	"time"
)

type PlayerState struct {
	PlayerID               string             `json:"playerId"`
	DisplayName            string             `json:"displayName"`
	Color                  Color              `json:"color"`
	WorldX                 float64            `json:"worldX"`
	WorldY                 float64            `json:"worldY"`
	CourseHeading          float64            `json:"courseHeading"`
	IsAlive                bool               `json:"isAlive"`
	SpeedEffect            *SpeedEffect       `json:"speedEffect"`
	EffectRemainingSeconds float64            `json:"effectRemainingSeconds"`
	EliminatedAtTick       *int64             `json:"eliminatedAtTick"`
	EliminationReason      *EliminationReason `json:"eliminationReason"`
	TouchedFieldIDs        []string           `json:"touchedFieldIds"`
}

func (player PlayerState) Distance() float64 { return math.Max(0, player.WorldX-SpawnX) }

type SimulationOutcome struct {
	Status    ResultStatus `json:"status"`
	EndReason EndReason    `json:"endReason"`
	WinnerID  *string      `json:"winnerId"`
}

// SimulationState is an internal checkpoint, not the public realtime wire DTO.
// Transport bindings, input acknowledgements and ownership epochs belong to the runtime.
type SimulationState struct {
	SchemaVersion int                `json:"schemaVersion"`
	CourseVersion int                `json:"courseVersion"`
	SessionID     string             `json:"sessionId"`
	HouseID       string             `json:"houseId"`
	StartedAt     time.Time          `json:"startedAt"`
	Tick          int64              `json:"tick"`
	CameraX       float64            `json:"cameraX"`
	NextGateIndex int                `json:"nextGateIndex"`
	Players       []PlayerState      `json:"players"`
	Outcome       *SimulationOutcome `json:"outcome"`
}

type WorldSnapshot struct {
	SimulationState
	ElapsedSeconds float64      `json:"elapsedSeconds"`
	CourseAngle    float64      `json:"courseAngle"`
	Gates          []Gate       `json:"gates"`
	SpeedFields    []SpeedField `json:"speedFields"`
}

func (simulation *Simulation) State() SimulationState {
	state := SimulationState{SchemaVersion: SimulationStateVersion, CourseVersion: CourseVersion, SessionID: simulation.sessionID,
		HouseID: simulation.houseID, StartedAt: simulation.startedAt, Tick: simulation.tick, CameraX: simulation.cameraX,
		NextGateIndex: simulation.nextGateIndex, Players: make([]PlayerState, len(simulation.players)), Outcome: copyOutcome(simulation.outcome)}
	for index, player := range simulation.players {
		state.Players[index] = player.stateCopy()
	}
	return state
}

func (simulation *Simulation) Snapshot() WorldSnapshot {
	elapsed := float64(simulation.tick) / PhysicsRateHz
	orientation, _ := CourseOrientationAt(elapsed)
	snapshot := WorldSnapshot{SimulationState: simulation.State(), ElapsedSeconds: elapsed, CourseAngle: orientation.Angle,
		Gates: make([]Gate, len(simulation.gates)), SpeedFields: make([]SpeedField, len(simulation.speedFields))}
	for index, entry := range simulation.gates {
		snapshot.Gates[index] = entry.gate
		snapshot.Gates[index].Sections = append([]PassageSection(nil), entry.gate.Sections...)
	}
	copy(snapshot.SpeedFields, simulation.speedFields)
	return snapshot
}

func RestoreSimulation(state SimulationState) (*Simulation, error) {
	if state.SchemaVersion != SimulationStateVersion || state.CourseVersion != CourseVersion || !validID(state.SessionID) || !validID(state.HouseID) ||
		state.StartedAt.IsZero() || state.Tick < 0 || state.Tick > MaximumMatchTicks || !finite(state.CameraX) || state.CameraX < 0 ||
		state.NextGateIndex < 2 || state.NextGateIndex > maximumCourseEntries || len(state.Players) < MinimumPlayers || len(state.Players) > MaximumPlayers {
		return nil, ErrInvalidSimulationState
	}
	travelLimit := float64(state.Tick) * BaseSpeed * BoostMultiplier / PhysicsRateHz
	if state.CameraX > math.Max(0, SpawnX+travelLimit-LeaderAnchorX)+2*RocketRadius {
		return nil, ErrInvalidSimulationState
	}
	threshold := state.CameraX + ViewportWidth + courseLookahead
	nextX := FirstGateX + float64(state.NextGateIndex)*GateSpacing
	lastX := nextX - GateSpacing
	// Generation occurs before movement; at most one physics step of lookahead can be pending.
	if lastX >= threshold || nextX < threshold-BaseSpeed*BoostMultiplier/PhysicsRateHz {
		return nil, ErrInvalidSimulationState
	}
	simulation := &Simulation{sessionID: state.SessionID, houseID: state.HouseID, startedAt: state.StartedAt.UTC(), tick: state.Tick,
		cameraX: state.CameraX, nextGateIndex: state.NextGateIndex, players: make([]rocket, len(state.Players)), outcome: copyOutcome(state.Outcome)}
	for index := 0; index < state.NextGateIndex; index++ {
		simulation.addCourseEntry(index)
	}
	colors, seen, aliveCount, aliveID := PlayerColors(), make(map[string]bool, len(state.Players)), 0, ""
	for index, player := range state.Players {
		if !validPlayerState(player, state.Tick, travelLimit) || seen[player.PlayerID] || player.Color != colors[index] ||
			(player.IsAlive && (IsBehindCamera(player.WorldX, state.CameraX) || player.WorldX-LeaderAnchorX > state.CameraX+1e-9)) {
			return nil, ErrInvalidSimulationState
		}
		seen[player.PlayerID] = true
		if state.Tick == 0 && (player.WorldX != SpawnX || player.WorldY != 105+float64(index)*150/float64(len(state.Players)-1) || player.SpeedEffect != nil || len(player.TouchedFieldIDs) != 0) {
			return nil, ErrInvalidSimulationState
		}
		if player.IsAlive {
			aliveCount++
			aliveID = player.PlayerID
		}
		entry := rocket{state: player, touchedFields: make(map[int]struct{}, len(player.TouchedFieldIDs))}
		entry.state.SpeedEffect = cloneValue(player.SpeedEffect)
		entry.state.EliminatedAtTick = cloneValue(player.EliminatedAtTick)
		entry.state.EliminationReason = cloneValue(player.EliminationReason)
		entry.state.TouchedFieldIDs = nil
		if len(player.TouchedFieldIDs) > len(simulation.speedFields) {
			return nil, ErrInvalidSimulationState
		}
		for _, id := range player.TouchedFieldIDs {
			fieldIndex, err := strconv.Atoi(strings.TrimPrefix(id, "field:"))
			if err != nil || id != "field:"+strconv.Itoa(fieldIndex) || fieldIndex < 0 || fieldIndex >= state.NextGateIndex || fieldIndex%5 == 4 {
				return nil, ErrInvalidSimulationState
			}
			if _, duplicate := entry.touchedFields[fieldIndex]; duplicate {
				return nil, ErrInvalidSimulationState
			}
			field, _ := SpeedFieldAt(fieldIndex)
			if field.WorldX+SpeedFieldRadius < state.CameraX-courseTrailingMargin {
				return nil, ErrInvalidSimulationState
			}
			entry.touchedFields[fieldIndex] = struct{}{}
		}
		point := Point{player.WorldX, player.WorldY}
		for _, gate := range simulation.gates {
			for _, polygon := range gate.polygons {
				resolved := resolveContact(point, RocketRadius, polygon)
				if math.Hypot(point.X-resolved.X, point.Y-resolved.Y) > 1e-7 {
					return nil, ErrInvalidSimulationState
				}
			}
		}
		simulation.players[index] = entry
	}
	if !validOutcome(state.Outcome, aliveCount, aliveID, state.Tick) {
		return nil, ErrInvalidSimulationState
	}
	return simulation, nil
}

func validPlayerState(player PlayerState, tick int64, travelLimit float64) bool {
	if !validID(player.PlayerID) || strings.TrimSpace(player.DisplayName) == "" || !finite(player.WorldX) || !finite(player.WorldY) ||
		!finite(player.CourseHeading) || math.Abs(player.CourseHeading) > math.Pi || !finite(player.EffectRemainingSeconds) ||
		math.Abs(player.WorldX-SpawnX) > travelLimit+2*RocketRadius || player.WorldY < RocketRadius-1e-9 || player.WorldY > TrackHeight-RocketRadius+1e-9 {
		return false
	}
	if player.SpeedEffect == nil {
		if player.EffectRemainingSeconds != 0 {
			return false
		}
	} else {
		_, duration := effectSettings(*player.SpeedEffect)
		if duration == 0 || player.EffectRemainingSeconds <= 0 || player.EffectRemainingSeconds > duration {
			return false
		}
	}
	if player.IsAlive {
		return player.EliminatedAtTick == nil && player.EliminationReason == nil
	}
	if player.EliminatedAtTick == nil || *player.EliminatedAtTick < 0 || *player.EliminatedAtTick > tick || player.EliminationReason == nil {
		return false
	}
	switch *player.EliminationReason {
	case EliminationBehindCamera, EliminationForfeit, EliminationConnectionExpired, EliminationMembershipRevoked:
		return true
	default:
		return false
	}
}

func validOutcome(outcome *SimulationOutcome, aliveCount int, aliveID string, tick int64) bool {
	if outcome == nil {
		return aliveCount >= MinimumPlayers && tick < MaximumMatchTicks
	}
	if outcome.Status == ResultCompleted {
		if outcome.EndReason == EndLastSurvivor {
			return aliveCount == 1 && outcome.WinnerID != nil && *outcome.WinnerID == aliveID
		}
		return outcome.EndReason == EndSimultaneousElimination && aliveCount == 0 && outcome.WinnerID == nil
	}
	return outcome.Status == ResultCancelled && outcome.WinnerID == nil && cancellationReason(outcome.EndReason) && aliveCount >= MinimumPlayers &&
		((outcome.EndReason == EndSessionExpired && tick == MaximumMatchTicks) || (outcome.EndReason != EndSessionExpired && tick < MaximumMatchTicks))
}

func copyOutcome(outcome *SimulationOutcome) *SimulationOutcome {
	if outcome == nil {
		return nil
	}
	copy := *outcome
	copy.WinnerID = cloneValue(outcome.WinnerID)
	return &copy
}
