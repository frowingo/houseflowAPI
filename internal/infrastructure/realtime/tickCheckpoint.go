package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
)

// checkpointState serializes checkpoint writes and the terminal release; it is
// never held by the physics scheduler.
type checkpointState struct {
	mutex    sync.Mutex
	released bool
	sequence int64
	critical string
	savedAt  time.Time
}

// protectCheckpoint stores a checkpoint when it carries a critical change, is
// forced, or the interval elapsed. No network I/O runs under the runtime lock.
func (room *managedRoom) protectCheckpoint(checkpoint *module.Checkpoint, force bool) error {
	if checkpoint == nil {
		return errors.New("runtime checkpoint missing")
	}
	state := &room.tick.checkpoint
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.released {
		return nil
	}
	if checkpoint.Sequence <= state.sequence {
		if checkpoint.CriticalKey == state.critical {
			return nil
		}
		return coordinationAbstract.ErrCheckpointOrder
	}
	if !force && checkpoint.CriticalKey == state.critical && time.Since(state.savedAt) < room.manager.options.CheckpointInterval {
		return nil
	}
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	payload, err := json.Marshal(checkpoint.State)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(room.ctx, min(room.manager.options.CommandTimeout, time.Until(*room.leaseDeadline.Load())))
	defer cancel()
	started := time.Now()
	err = room.manager.checkpoints.SaveRuntimeCheckpoint(ctx, room.lease, coordinationAbstract.RuntimeCheckpoint{Epoch: checkpoint.Epoch, Sequence: checkpoint.Sequence, CapturedAt: checkpoint.CapturedAt, Payload: payload}, room.manager.options.CheckpointTTL)
	room.manager.options.Metrics.checkpointDuration.Observe(time.Since(started))
	if err != nil {
		room.manager.options.Metrics.checkpointFailures.Add(1)
		return err
	}
	room.manager.options.Metrics.checkpoints.Add(1)
	room.manager.options.Metrics.checkpointBytes.Add(uint64(len(payload)))
	state.sequence, state.critical, state.savedAt = checkpoint.Sequence, checkpoint.CriticalKey, time.Now()
	return nil
}

// releaseCheckpoint deletes the checkpoint of a committed match. Like lease
// release it outlives room cancellation, so shutdown right after a commit still
// cleans up; frames still queued for publication must not recreate it.
func (room *managedRoom) releaseCheckpoint() error {
	state := &room.tick.checkpoint
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.released = true
	ctx, cancel := context.WithTimeout(context.Background(), roomCleanupTimeout)
	defer cancel()
	return room.manager.checkpoints.DeleteRuntimeCheckpoint(ctx, room.lease)
}

// restoreRuntime resumes a started match from its checkpoint. Missing, corrupt
// or foreign state never spawns new physics: the match is cancelled instead.
func (room *managedRoom) restoreRuntime(snapshot gameDomain.SessionSnapshot) (module.TickRuntime, bool, error) {
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	defer cancel()
	stored, found, err := room.manager.checkpoints.LoadRuntimeCheckpoint(ctx, room.lease)
	if err != nil && (errors.Is(err, coordinationAbstract.ErrUnavailable) || errors.Is(err, coordinationAbstract.ErrLeaseLost) || ctx.Err() != nil) {
		return nil, false, err
	}
	if found && err == nil {
		params := module.RestoreParams{Session: snapshot, Epoch: room.runtimeOwner.Generation, Now: time.Now(), Stored: module.Checkpoint{Epoch: stored.Epoch, Sequence: stored.Sequence, CapturedAt: stored.CapturedAt}}
		if room.runtimeOwner.CompletionTrigger != nil {
			params.CancelReason = module.EndCancelledByUser
		} else if time.Since(stored.CapturedAt) > room.manager.options.RecoveryMaxAge {
			params.CancelReason = module.EndRecoveryFailed
		}
		if runtime, err := room.tick.game.RestoreRuntime(stored.Payload, params); err == nil {
			return runtime, true, nil
		}
	}
	if room.manager.options.MatchRepository == nil {
		return nil, false, gameAbstract.ErrRuntimeRecoveryRequired
	}
	reason := module.EndRecoveryFailed
	if room.runtimeOwner.CompletionTrigger != nil {
		reason = module.EndCancelledByUser
	}
	result, err := room.tick.game.CancellationResult(snapshot, reason, time.Now())
	if err != nil {
		return nil, false, err
	}
	room.completion.result = &result
	return nil, true, nil
}
