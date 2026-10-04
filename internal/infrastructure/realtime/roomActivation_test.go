package realtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
)

// Only the remote-observer path is allowed: an observer must never claim ownership.
type activationCoordinator struct {
	coordinationAbstract.Coordinator
	coordinationAbstract.GameplayCoordinator
	lease            coordinationAbstract.RoomLease
	ownerExists      bool
	ownerReads       int
	lossOnValidation bool
}

func (coordinator *activationCoordinator) InstanceID() string { return "observer" }
func (coordinator *activationCoordinator) AcquireRoom(context.Context, string, time.Duration) (coordinationAbstract.RoomLease, bool, error) {
	return coordinationAbstract.RoomLease{}, false, nil
}
func (coordinator *activationCoordinator) CurrentRoomOwner(context.Context, string) (coordinationAbstract.RoomLease, bool, error) {
	coordinator.ownerReads++
	return coordinator.lease, coordinator.ownerExists && !(coordinator.lossOnValidation && coordinator.ownerReads > 1), nil
}

type activationRepository struct {
	gameAbstract.GameSessionRepository
	gameAbstract.RuntimeOwnerRepository
	session     *gameDomain.GameSession
	mutex       sync.Mutex
	owner       gameAbstract.RuntimeOwner
	reads       int
	pendingRead chan struct{}
}

func (repository *activationRepository) FindByID(context.Context, string) (*gameDomain.GameSession, error) {
	return repository.session, nil
}
func (repository *activationRepository) FindRuntimeOwner(context.Context, string) (gameAbstract.RuntimeOwner, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.reads++
	if repository.reads == 2 {
		close(repository.pendingRead)
	}
	return repository.owner, nil
}

func newRemoteActivation(t *testing.T) (*RoomManager, *activationRepository, *activationCoordinator) {
	t.Helper()
	definition := houseRockets.Definition()
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{
		SessionID: "activation-room", HouseID: "house", GameKey: definition.GameKey,
		ProtocolVersion: definition.ProtocolVersion, Mode: definition.Mode,
		Rules: definition.Rules, CreatedBy: "player", CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := &activationRepository{session: session, pendingRead: make(chan struct{})}
	coordinator := &activationCoordinator{ownerExists: true, lease: coordinationAbstract.RoomLease{
		RoomID: session.Snapshot().SessionID, OwnerInstanceID: "owner", LeaseID: "new-lease",
		FencingToken: 1, ExpiresAt: time.Now().Add(time.Minute),
	}}
	manager := &RoomManager{repository: repository, coordinator: coordinator, options: normalizeOptions(RoomManagerOptions{})}
	return manager, repository, coordinator
}

func TestRemoteActivationWaitsForDurableOwnerPublication(t *testing.T) {
	manager, repository, coordinator := newRemoteActivation(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	type result struct {
		ownership RoomOwnership
		err       error
	}
	done := make(chan result, 1)
	go func() {
		ownership, err := manager.activateRoom(ctx, ctx, coordinator.lease.RoomID)
		done <- result{ownership, err}
	}()
	select {
	case <-repository.pendingRead:
	case <-ctx.Done():
		t.Fatal("observer never read the pending durable owner")
	}
	select {
	case got := <-done:
		t.Fatalf("activation returned before owner publication: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}
	repository.mutex.Lock()
	repository.owner = gameAbstract.RuntimeOwner{SessionID: coordinator.lease.RoomID, Generation: 7,
		OwnerInstanceID: coordinator.lease.OwnerInstanceID, LeaseID: coordinator.lease.LeaseID}
	repository.mutex.Unlock()
	select {
	case got := <-done:
		if got.err != nil || got.ownership.Local || got.ownership.RuntimeEpoch != 7 || got.ownership.OwnerInstanceID != "owner" {
			t.Fatalf("remote activation: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("activation did not observe published owner")
	}
}

func TestRemoteActivationStopsWhenOwnerPublicationTimesOut(t *testing.T) {
	manager, _, coordinator := newRemoteActivation(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := manager.activateRoom(ctx, ctx, coordinator.lease.RoomID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("owner wait: %v", err)
	}
}

func TestRemoteActivationRejectsReleasedLease(t *testing.T) {
	manager, repository, coordinator := newRemoteActivation(t)
	repository.owner = gameAbstract.RuntimeOwner{Generation: 7, OwnerInstanceID: "owner", LeaseID: "new-lease"}
	// The initial lease lookup succeeds, then the lease disappears during validation.
	coordinator.lossOnValidation = true
	_, err := manager.activateRoom(context.Background(), context.Background(), coordinator.lease.RoomID)
	if !errors.Is(err, coordinationAbstract.ErrLeaseLost) {
		t.Fatalf("released lease: %v", err)
	}
}
