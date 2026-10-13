package tests

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"

	"github.com/redis/go-redis/v9"
)

// TestRoomLifecycleRedisFootprint characterizes which Redis keys outlive a
// finished room. The expected set is a deliberate baseline: a retention change
// must update it together with the change.
func TestRoomLifecycleRedisFootprint(t *testing.T) {
	redisURL := prepareRedis(t)
	coordinator := newTestCoordinator(t, redisURL, "footprint-instance")
	ctx, cancel := context.WithTimeout(context.Background(), coordinationTestTimeout)
	defer cancel()

	lease, acquired, err := coordinator.AcquireRoom(ctx, "footprint-room", 10*time.Second)
	if err != nil || !acquired {
		t.Fatalf("acquire room: acquired=%t err=%v", acquired, err)
	}
	presence := coordinationAbstract.Presence{RoomID: lease.RoomID, UserID: "footprint-user", ConnectionID: "footprint-connection"}
	if err := coordinator.TouchPresence(ctx, presence, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	checkpoint := coordinationAbstract.RuntimeCheckpoint{Epoch: 1, Sequence: 1, CapturedAt: time.Now().UTC(), Payload: []byte(`{}`)}
	if err := coordinator.SaveRuntimeCheckpoint(ctx, lease, checkpoint, time.Minute); err != nil {
		t.Fatal(err)
	}

	// The same cleanup a finished room performs.
	if err := coordinator.DeleteRuntimeCheckpoint(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.RemovePresence(ctx, lease.RoomID, presence.ConnectionID); err != nil {
		t.Fatal(err)
	}
	if released, err := coordinator.ReleaseRoom(ctx, lease); err != nil || !released {
		t.Fatalf("release room: released=%t err=%v", released, err)
	}

	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	var persistent, expiring []string
	iterator := client.Scan(ctx, 0, "*", 100).Iterator()
	for iterator.Next(ctx) {
		key := iterator.Val()
		ttl, err := client.TTL(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if ttl < 0 {
			persistent = append(persistent, key)
		} else {
			expiring = append(expiring, key)
		}
	}
	if err := iterator.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(persistent)
	sort.Strings(expiring)
	t.Logf("ROOM_REDIS_FOOTPRINT persistent=[%s] expiring=[%s]", strings.Join(persistent, " "), strings.Join(expiring, " "))
	if len(persistent) != 1 || !strings.HasSuffix(persistent[0], ":fence") {
		t.Fatalf("keys without TTL after room cleanup = %v, want only the room fence", persistent)
	}
}
