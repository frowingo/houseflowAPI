package realtime

import (
	"context"
	"encoding/json"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"

	"github.com/google/uuid"
)

func (room *managedRoom) freezePlayers(snapshot gameDomain.SessionSnapshot) {
	room.lobbyMutex.Lock()
	defer room.lobbyMutex.Unlock()
	for _, player := range snapshot.Players {
		eligibleRunning := snapshot.State == gameDomain.SessionRunning && !player.ReadyAt.IsZero() && (player.LeftAt.IsZero() || !player.LeftAt.Before(snapshot.StartedAt.Add(-snapshot.Rules.CountdownDuration)))
		if !eligibleRunning && player.State != gameDomain.PlayerReady && player.State != gameDomain.PlayerPlaying {
			continue
		}
		name := room.members[player.PlayerID]
		if name == "" {
			name = "Player"
		}
		room.frozenPlayers = append(room.frozenPlayers, gameParticipant{PlayerID: player.PlayerID, DisplayName: name})
	}
}

func (room *managedRoom) publishPreviewSnapshot(connectionID, messageID string) error {
	room.gameMutex.Lock()
	defer room.gameMutex.Unlock()
	return room.publishPreviewLocked(connectionID, messageID)
}

type gamePreviewFrame struct {
	houseRockets.RuntimeFrame
	CountdownEndsAt *time.Time `json:"countdownEndsAt"`
}

func (room *managedRoom) publishPreviewLocked(connectionID, messageID string) error {
	snapshot := room.snapshot.Load()
	if snapshot == nil || snapshot.State != gameDomain.SessionCountdown {
		return nil
	}
	if len(room.frozenPlayers) == 0 {
		room.freezePlayers(*snapshot)
	}
	simulation, err := houseRockets.NewSimulation(houseRockets.NewSimulationParams{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, StartedAt: snapshot.CountdownEndsAt, Players: room.frozenPlayers})
	if err != nil {
		return err
	}
	room.previewSequence++
	frame := gamePreviewFrame{RuntimeFrame: houseRockets.RuntimeFrame{RuntimeEpoch: room.runtimeOwner.Generation, StateSequence: room.previewSequence, Phase: houseRockets.PhaseCountdown, World: simulation.Snapshot(), Controls: []houseRockets.RuntimePlayerControl{}}, CountdownEndsAt: &snapshot.CountdownEndsAt}
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if messageID == "" {
		messageID = uuid.NewString()
	}
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	defer cancel()
	return room.manager.coordinator.PublishRoomEvent(ctx, room.lease, coordinationAbstract.MessageEnvelope{MessageID: messageID, Type: GameRuntimeSnapshotEventType, ConnectionID: connectionID, Sequence: room.previewSequence, Payload: payload})
}
