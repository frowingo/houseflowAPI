package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
)

const (
	defaultCheckpointInterval   = time.Second
	defaultCheckpointTTL        = 10 * time.Minute
	defaultRecoveryMaxAge       = 30 * time.Second
	defaultRecoveryScanInterval = 5 * time.Second
	defaultRecoveryScanBatch    = 100
	defaultFinalizationTimeout  = 30 * time.Second
)

// No network I/O runs inside the simulation lock. One mutex serializes the
// checkpoint writer and terminal persistence barrier, not the physics scheduler.
func (room *managedRoom) protectCheckpoint(checkpoint *houseRockets.RuntimeCheckpoint, force bool) error {
	if checkpoint == nil {
		return errors.New("runtime checkpoint missing")
	}
	store, ok := room.manager.coordinator.(coordinationAbstract.RuntimeCheckpointStore)
	if !ok {
		return errors.New("runtime checkpoint store required")
	}
	room.checkpointMutex.Lock()
	defer room.checkpointMutex.Unlock()
	critical := checkpoint.CriticalKey()
	if checkpoint.Sequence <= room.checkpointSequence {
		if critical == room.checkpointCritical {
			return nil
		}
		return coordinationAbstract.ErrCheckpointOrder
	}
	if !force && critical == room.checkpointCritical && time.Since(room.checkpointAt) < room.manager.options.CheckpointInterval {
		return nil
	}
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(room.ctx, min(room.manager.options.CommandTimeout, time.Until(*room.leaseDeadline.Load())))
	defer cancel()
	started := time.Now()
	err = store.SaveRuntimeCheckpoint(ctx, room.lease, coordinationAbstract.RuntimeCheckpoint{Epoch: checkpoint.Epoch, Sequence: checkpoint.Sequence, CapturedAt: checkpoint.CapturedAt, Payload: payload}, room.manager.options.CheckpointTTL)
	room.manager.options.Metrics.checkpointDuration.Observe(time.Since(started))
	if err != nil {
		room.manager.options.Metrics.checkpointFailures.Add(1)
		return err
	}
	room.manager.options.Metrics.checkpoints.Add(1)
	room.manager.options.Metrics.checkpointBytes.Add(uint64(len(payload)))
	room.checkpointSequence, room.checkpointCritical, room.checkpointAt = checkpoint.Sequence, critical, time.Now()
	return nil
}

func (room *managedRoom) restoreGameRuntime(snapshot gameDomain.SessionSnapshot) (*houseRockets.Runtime, bool, error) {
	store, ok := room.manager.coordinator.(coordinationAbstract.RuntimeCheckpointStore)
	if !ok {
		return nil, false, errors.New("runtime checkpoint store required")
	}
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	defer cancel()
	stored, found, err := store.LoadRuntimeCheckpoint(ctx, room.lease)
	if err != nil && (errors.Is(err, coordinationAbstract.ErrUnavailable) || errors.Is(err, coordinationAbstract.ErrLeaseLost) || ctx.Err() != nil) {
		return nil, false, err
	}
	var checkpoint houseRockets.RuntimeCheckpoint
	valid := found && err == nil && json.Unmarshal(stored.Payload, &checkpoint) == nil && stored.Epoch == checkpoint.Epoch && stored.Sequence == checkpoint.Sequence && stored.CapturedAt.Equal(checkpoint.CapturedAt) && checkpoint.World.SessionID == snapshot.SessionID && checkpoint.World.HouseID == snapshot.HouseID && checkpoint.World.StartedAt.Truncate(time.Millisecond).Equal(snapshot.StartedAt.Truncate(time.Millisecond)) && checkpoint.CapturedAt.Before(time.Now().Add(time.Second))
	var runtime *houseRockets.Runtime
	if valid {
		runtime, err = houseRockets.RestoreRuntime(checkpoint, room.runtimeOwner.Generation, time.Now())
		valid = err == nil && houseRockets.ValidateCheckpointSession(checkpoint, snapshot) == nil
	}
	if valid {
		// A terminal proposal is immutable even when finalization retries outlive
		// the movement recovery budget. Never replace a proposed winner.
		if checkpoint.World.Outcome == nil {
			if room.runtimeOwner.CompletionTrigger != nil {
				_ = runtime.Cancel(houseRockets.EndCancelledByUser)
			} else if time.Since(checkpoint.CapturedAt) > room.manager.options.RecoveryMaxAge {
				_ = runtime.Cancel(houseRockets.EndRecoveryFailed)
			}
		}
		return runtime, true, nil
	}
	if room.manager.options.MatchRepository == nil {
		return nil, false, gameAbstract.ErrRuntimeRecoveryRequired
	}
	reason := houseRockets.EndRecoveryFailed
	if room.runtimeOwner.CompletionTrigger != nil {
		reason = houseRockets.EndCancelledByUser
	}
	result, err := houseRockets.RecoveryCancellationResult(snapshot, reason, time.Now())
	if err != nil {
		return nil, false, err
	}
	room.pendingResult = &result
	return nil, true, nil
}

// One short-lived elected scanner pages the existing active-pointer index. It
// does not depend on sockets or commands and does not scan historical sessions.
func (manager *RoomManager) recoveryLoop(ctx context.Context, scanner gameAbstract.ActiveGameSessionScanner) {
	defer manager.workers.Done()
	ticker := time.NewTicker(manager.options.RecoveryScanInterval)
	defer ticker.Stop()
	after := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		workCtx, cancel := context.WithTimeout(ctx, manager.options.CommandTimeout)
		lease, acquired, err := manager.coordinator.AcquireRoom(workCtx, "runtimeRecoveryScanner", manager.options.LeaseTTL)
		if err != nil || !acquired {
			cancel()
			continue
		}
		page, err := scanner.ListActiveSessions(workCtx, after, manager.options.RecoveryScanBatch)
		if err == nil {
			pageComplete := true
			for _, session := range page {
				if workCtx.Err() != nil {
					pageComplete = false
					break
				}
				after = session.SessionID
				if !supportsGameRuntime(session.GameKey) || manager.localRoom(session.SessionID) != nil {
					continue
				}
				_, exists, err := manager.coordinator.CurrentRoomOwner(workCtx, session.SessionID)
				if err != nil {
					pageComplete = false
					break
				}
				if exists {
					continue
				}
				stored, err := manager.repository.FindByID(workCtx, session.SessionID)
				if err != nil {
					manager.report(session.SessionID, err)
					continue
				}
				if stored.State() == gameDomain.SessionLobby {
					continue
				}
				if _, err := manager.EnsureRoom(workCtx, session.SessionID); err != nil && !errors.Is(err, ErrTerminalRoom) && !errors.Is(err, gameAbstract.ErrRuntimeOwnerConflict) {
					manager.report(session.SessionID, err)
				}
			}
			if pageComplete && len(page) < manager.options.RecoveryScanBatch {
				after = ""
			}
		} else {
			manager.report("", err)
		}
		cancel()
		manager.releaseLease(lease)
	}
}
