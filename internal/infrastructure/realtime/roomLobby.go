package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

const GameRoomAccessEventType = "gameRuntime.access"
const lobbyHeartbeatInput = "lobbyHeartbeat"

// lobbyState is the realtime view of a room's players: current house members,
// application heartbeats of ready players and the roster frozen at countdown.
type lobbyState struct {
	mutex    sync.Mutex
	members  map[string]string
	lastSeen map[string]time.Time
	frozen   []module.Player
}

func (room *managedRoom) refreshParticipants(snapshot gameDomain.SessionSnapshot) error {
	room.snapshot.Store(&snapshot)
	if room.manager.options.ParticipantDirectory == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	defer cancel()
	participants, err := room.manager.options.ParticipantDirectory.ReadHouseParticipants(ctx, snapshot.HouseID)
	if err != nil {
		return err
	}
	names := make(map[string]string, len(participants))
	ids := make([]string, 0, len(participants))
	for _, participant := range participants {
		names[participant.PlayerID] = participant.DisplayName
		ids = append(ids, participant.PlayerID)
	}
	room.lobby.mutex.Lock()
	// Sockets only act on a membership change; a new owner always announces
	// once because revocations may have happened during the handoff.
	changed := room.lobby.members == nil || !sameMembers(room.lobby.members, names)
	room.lobby.members = names
	room.lobby.mutex.Unlock()
	if !changed {
		return nil
	}
	payload, _ := json.Marshal(ids)
	return room.manager.coordinator.PublishRoomEvent(ctx, room.lease, coordinationAbstract.MessageEnvelope{MessageID: fmt.Sprintf("access:%s:%d", snapshot.SessionID, time.Now().UnixNano()), Type: GameRoomAccessEventType, Payload: payload})
}

func sameMembers(current, next map[string]string) bool {
	if len(current) != len(next) {
		return false
	}
	for id := range next {
		if _, exists := current[id]; !exists {
			return false
		}
	}
	return true
}

// recordLobbyHeartbeat keeps a ready player from being excluded as
// disconnected before the match starts.
func (room *managedRoom) recordLobbyHeartbeat(input module.ControlInput) error {
	if input.RuntimeEpoch != room.runtimeOwner.Generation {
		return module.ErrStaleInput
	}
	if time.Since(input.CreatedAt) > room.tick.game.Timing().InputLifetime {
		return module.ErrInputExpired
	}
	room.lobby.mutex.Lock()
	defer room.lobby.mutex.Unlock()
	if room.lobby.lastSeen == nil {
		room.lobby.lastSeen = make(map[string]time.Time)
	}
	if room.manager.options.ParticipantDirectory != nil {
		if _, member := room.lobby.members[input.PlayerID]; !member {
			return module.ErrControlRequired
		}
	}
	if snapshot := room.snapshot.Load(); snapshot != nil {
		for _, player := range snapshot.Players {
			if player.PlayerID == input.PlayerID {
				room.lobby.lastSeen[input.PlayerID] = time.Now()
				return nil
			}
		}
	}
	return nil
}

// reconcilePlayers excludes revoked members and ready players whose lobby
// heartbeat expired, grouped by reason.
func (room *managedRoom) reconcilePlayers(snapshot gameDomain.SessionSnapshot, at time.Time) (gameDomain.SessionSnapshot, error) {
	if room.manager.options.ParticipantDirectory == nil || !room.managesPlayers() {
		return snapshot, nil
	}
	if err := room.refreshParticipants(snapshot); err != nil {
		return snapshot, err
	}
	// Exclusion precedes the deadline transition. Do not write an UpdatedAt
	// later than an overdue deadline while leaving that transition pending.
	if deadline, exists := sessionDeadline(snapshot); exists && deadline.Before(at) {
		at = deadline
	}
	if at.Before(snapshot.UpdatedAt) {
		at = snapshot.UpdatedAt
	}
	controlTimeout := room.tick.game.Timing().ControlTimeout
	groups := map[string][]string{"membershipRevoked": {}, "connectionExpired": {}}
	room.lobby.mutex.Lock()
	for _, player := range snapshot.Players {
		if player.State == gameDomain.PlayerLeft || player.State == gameDomain.PlayerFinished {
			continue
		}
		if _, member := room.lobby.members[player.PlayerID]; !member {
			groups["membershipRevoked"] = append(groups["membershipRevoked"], player.PlayerID)
			continue
		}
		if player.State == gameDomain.PlayerReady {
			lastSeen := room.lobby.lastSeen[player.PlayerID]
			if lastSeen.Before(player.ReadyAt) {
				lastSeen = player.ReadyAt
			}
			if at.Sub(lastSeen) >= controlTimeout {
				groups["connectionExpired"] = append(groups["connectionExpired"], player.PlayerID)
			}
		}
	}
	room.lobby.mutex.Unlock()
	for _, reason := range []string{"membershipRevoked", "connectionExpired"} {
		ids := groups[reason]
		if len(ids) == 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
		updated, err := cqrs.Send[gameDomain.SessionSnapshot](ctx, room.manager.sender, gameCommands.ReconcileGameSessionPlayersCommand{CommandID: "exclude:" + uuid.NewString(), SessionID: snapshot.SessionID, PlayerIDs: ids, Reason: reason, At: at})
		cancel()
		if err != nil {
			if helpers.IsApplicationError(err, "game.error.session_conflict") {
				ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
				stored, loadErr := room.manager.repository.FindByID(ctx, snapshot.SessionID)
				cancel()
				if loadErr != nil {
					return snapshot, loadErr
				}
				return stored.Snapshot(), nil
			}
			return snapshot, err
		}
		snapshot = updated
		if snapshot.State == gameDomain.SessionCancelled {
			break
		}
	}
	return snapshot, nil
}

func (room *managedRoom) rosterIDs(snapshot gameDomain.SessionSnapshot) ([]string, []string) {
	playing := make([]string, 0, len(snapshot.Players))
	for _, player := range snapshot.Players {
		if player.State == gameDomain.PlayerPlaying {
			playing = append(playing, player.PlayerID)
		}
	}
	members := make([]string, 0, len(snapshot.Players))
	room.lobby.mutex.Lock()
	defer room.lobby.mutex.Unlock()
	if room.manager.options.ParticipantDirectory == nil {
		for _, player := range snapshot.Players {
			members = append(members, player.PlayerID)
		}
	} else {
		for id := range room.lobby.members {
			members = append(members, id)
		}
	}
	return playing, members
}

// freezePlayers selects the ready roster once; a running session recovered by
// a new owner rebuilds it from players who were ready before the countdown.
func (room *managedRoom) freezePlayers(snapshot gameDomain.SessionSnapshot) {
	room.lobby.mutex.Lock()
	defer room.lobby.mutex.Unlock()
	for _, player := range snapshot.Players {
		eligibleRunning := snapshot.State == gameDomain.SessionRunning && !player.ReadyAt.IsZero() && (player.LeftAt.IsZero() || !player.LeftAt.Before(snapshot.StartedAt.Add(-snapshot.Rules.CountdownDuration)))
		if !eligibleRunning && player.State != gameDomain.PlayerReady && player.State != gameDomain.PlayerPlaying {
			continue
		}
		name := room.lobby.members[player.PlayerID]
		if name == "" {
			name = "Player"
		}
		room.lobby.frozen = append(room.lobby.frozen, module.Player{PlayerID: player.PlayerID, DisplayName: name})
	}
}
