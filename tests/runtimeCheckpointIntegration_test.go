package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
)

func TestRuntimeCheckpointFencesLeaseAndOrdersEpochSequence(t *testing.T) {
	url := prepareRedis(t)
	first := newTestCoordinator(t, url, "checkpointFirst")
	second := newTestCoordinator(t, url, "checkpointSecond")
	ctx := context.Background()
	lease, acquired, err := first.AcquireRoom(ctx, "checkpointRoom", time.Second)
	if err != nil || !acquired {
		t.Fatal(err)
	}
	checkpoint := coordinationAbstract.RuntimeCheckpoint{Epoch: 9007199254740993, Sequence: 9007199254740993, CapturedAt: time.Now().UTC(), Payload: []byte(`{"opaqueGameState":true}`)}
	if err := first.SaveRuntimeCheckpoint(ctx, lease, checkpoint, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, _, err := second.LoadRuntimeCheckpoint(ctx, lease); !errors.Is(err, coordinationAbstract.ErrLeaseLost) {
		t.Fatalf("non-owner loaded private checkpoint: %v", err)
	}
	if err := second.DeleteRuntimeCheckpoint(ctx, lease); !errors.Is(err, coordinationAbstract.ErrLeaseLost) {
		t.Fatalf("non-owner deleted checkpoint: %v", err)
	}
	older := checkpoint
	older.Sequence--
	if err := first.SaveRuntimeCheckpoint(ctx, lease, older, time.Minute); !errors.Is(err, coordinationAbstract.ErrCheckpointOrder) {
		t.Fatalf("older sequence accepted: %v", err)
	}
	older.Epoch--
	older.Sequence++
	if err := first.SaveRuntimeCheckpoint(ctx, lease, older, time.Minute); !errors.Is(err, coordinationAbstract.ErrCheckpointOrder) {
		t.Fatalf("older epoch accepted: %v", err)
	}
	if _, err := first.ReleaseRoom(ctx, lease); err != nil {
		t.Fatal(err)
	}
	newLease, acquired, err := second.AcquireRoom(ctx, lease.RoomID, time.Second)
	if err != nil || !acquired {
		t.Fatal(err)
	}
	loaded, found, err := second.LoadRuntimeCheckpoint(ctx, newLease)
	if err != nil || !found || loaded.Epoch != checkpoint.Epoch || loaded.Sequence != checkpoint.Sequence {
		t.Fatalf("takeover lost state: %+v %t %v", loaded, found, err)
	}
	checkpoint.Epoch++
	checkpoint.Sequence++
	if err := second.SaveRuntimeCheckpoint(ctx, newLease, checkpoint, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveRuntimeCheckpoint(ctx, lease, checkpoint, time.Minute); !errors.Is(err, coordinationAbstract.ErrLeaseLost) {
		t.Fatalf("old owner write accepted: %v", err)
	}
	if err := first.DeleteRuntimeCheckpoint(ctx, lease); !errors.Is(err, coordinationAbstract.ErrLeaseLost) {
		t.Fatalf("old owner delete accepted: %v", err)
	}
	if err := second.DeleteRuntimeCheckpoint(ctx, newLease); err != nil {
		t.Fatal(err)
	}
	if _, found, err := second.LoadRuntimeCheckpoint(ctx, newLease); err != nil || found {
		t.Fatalf("checkpoint cleanup failed: %t %v", found, err)
	}
}
