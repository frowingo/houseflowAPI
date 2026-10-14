package realtime

import (
	"context"
	"errors"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
)

const (
	defaultCheckpointInterval   = time.Second
	defaultCheckpointTTL        = 10 * time.Minute
	defaultRecoveryMaxAge       = 30 * time.Second
	defaultRecoveryScanInterval = 5 * time.Second
	defaultRecoveryScanBatch    = 100
	defaultFinalizationTimeout  = 30 * time.Second
)

// One short-lived elected scanner pages the active-slot index. It does not
// depend on sockets or commands and does not scan historical sessions.
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
				after = session.Cursor
				// A running turn game needs an owner to expire turns even when
				// nobody is connected, so the house's active slot is released.
				if manager.realtimeGame(session.GameKey) == nil || manager.localRoom(session.SessionID) != nil {
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
