package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"github.com/google/uuid"
)

const GameRoomAccessEventType = "gameRuntime.access"
const lobbyHeartbeatInput = "lobbyHeartbeat"

type gameParticipant = houseRockets.PlayerIdentity

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
	room.lobbyMutex.Lock()
	room.members = names
	room.lobbyMutex.Unlock()
	payload, _ := json.Marshal(ids)
	return room.manager.coordinator.PublishRoomEvent(ctx, room.lease, coordinationAbstract.MessageEnvelope{MessageID: fmt.Sprintf("access:%s:%d", snapshot.SessionID, time.Now().UnixNano()), Type: GameRoomAccessEventType, Payload: payload})
}

func (room *managedRoom) reconcilePlayers(snapshot gameDomain.SessionSnapshot, at time.Time) (gameDomain.SessionSnapshot, error) {
	if room.manager.options.ParticipantDirectory == nil || !supportsGameRuntime(snapshot.GameKey) {
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
	groups := map[string][]string{"membershipRevoked": {}, "connectionExpired": {}}
	room.lobbyMutex.Lock()
	for _, player := range snapshot.Players {
		if player.State == gameDomain.PlayerLeft || player.State == gameDomain.PlayerFinished {
			continue
		}
		if _, member := room.members[player.PlayerID]; !member {
			groups["membershipRevoked"] = append(groups["membershipRevoked"], player.PlayerID)
			continue
		}
		if player.State == gameDomain.PlayerReady {
			lastSeen := room.lobbyLastSeen[player.PlayerID]
			if lastSeen.Before(player.ReadyAt) {
				lastSeen = player.ReadyAt
			}
			if at.Sub(lastSeen) >= houseRockets.ControlTimeout {
				groups["connectionExpired"] = append(groups["connectionExpired"], player.PlayerID)
			}
		}
	}
	room.lobbyMutex.Unlock()
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
	room.lobbyMutex.Lock()
	defer room.lobbyMutex.Unlock()
	if room.manager.options.ParticipantDirectory == nil {
		for _, player := range snapshot.Players {
			members = append(members, player.PlayerID)
		}
	} else {
		for id := range room.members {
			members = append(members, id)
		}
	}
	return playing, members
}
