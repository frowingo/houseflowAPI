package tests

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	ws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	"houseflowApi/internal/infrastructure/middleware"
	"houseflowApi/internal/infrastructure/realtime"
)

func TestRealtimeUpgradeReservationExpiresAndShutdownReleasesPending(t *testing.T) {
	fixture := newConfiguredRocketsGatewayFixture(t, houseRockets.Definition().Rules, 1, 1)
	session := persistRocketsSession(t, fixture.gameSessionApplicationFixture, false)
	service := fixture.services[0]
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	// Exercise an accepted HTTP preflight whose next handler never hijacks the
	// socket. Its capacity ticket must not remain reserved indefinitely.
	app.Get("/:sessionId/realtime", middleware.AuthRequired(fixture.jwt), service.UpgradeMiddleware(), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusOK)
	})
	preflight := func() int {
		request := httptest.NewRequest(http.MethodGet, "/"+session.SessionID+"/realtime?protocolVersion=2", nil)
		request.Header.Set("Connection", "Upgrade")
		request.Header.Set("Upgrade", "websocket")
		request.Header.Set("Authorization", "Bearer "+generateGatewayToken(t, fixture.jwt, fixture.ownerID))
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if status := preflight(); status != http.StatusOK || service.Metrics().PendingConnections != 1 {
		t.Fatalf("missing pending ticket: status=%d metrics=%+v", status, service.Metrics())
	}
	if status := preflight(); status != http.StatusServiceUnavailable {
		t.Fatalf("pending upgrade bypassed capacity: %d", status)
	}
	eventually(t, 6*time.Second, func() bool { return service.Metrics().PendingConnections == 0 })
	if status := preflight(); status != http.StatusOK {
		t.Fatalf("expired ticket could not be reused: %d", status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if snapshot := service.Metrics(); snapshot.PendingConnections != 0 || snapshot.PendingActivations != 0 || snapshot.ActiveRooms != 0 {
		t.Fatalf("shutdown retained capacity: %+v", snapshot)
	}
}

func TestRealtimeConnectionAdmissionBoundsConcurrentUpgradesAndReleases(t *testing.T) {
	fixture := newConfiguredRocketsGatewayFixture(t, houseRockets.Definition().Rules, 1, 3)
	session := persistRocketsSession(t, fixture.gameSessionApplicationFixture, false)
	endpoint := "ws" + fixture.urls[0][4:] + "/api/v1/game/" + session.SessionID + "/realtime?protocolVersion=2"
	var mutex sync.Mutex
	var connections []*ws.Conn
	var failures []error
	var rejected int
	var workers sync.WaitGroup
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			header := http.Header{"Authorization": []string{"Bearer " + generateGatewayToken(t, fixture.jwt, fixture.ownerID)}}
			connection, response, err := ws.DefaultDialer.Dial(endpoint, header)
			mutex.Lock()
			defer mutex.Unlock()
			if err == nil {
				connections = append(connections, connection)
				return
			}
			if response != nil {
				defer response.Body.Close()
				if response.StatusCode == 503 && response.Header.Get("Retry-After") == "1" {
					rejected++
					return
				}
			}
			failures = append(failures, err)
		}()
	}
	workers.Wait()
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	if len(failures) > 0 || len(connections) != 3 || rejected != 9 {
		t.Fatalf("admission: accepted=%d rejected=%d failures=%v", len(connections), rejected, failures)
	}
	if snapshot := fixture.services[0].Metrics(); snapshot.ActiveConnections > 3 || snapshot.AdmissionRejected < 9 {
		t.Fatalf("capacity metrics: %+v", snapshot)
	}
	for _, connection := range connections {
		_ = connection.Close()
	}
	eventually(t, time.Second, func() bool { return fixture.services[0].Metrics().ActiveConnections == 0 })
	// Existing room remains reachable at the room limit; freed socket admission
	// can be reused without creating a duplicate active session.
	fixture.connect(t, 0, session.SessionID, fixture.ownerID)
}

func TestRealtimeRoomAdmissionRejectsLocalOwnershipButAllowsRemoteRooms(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	rockets := persistRocketsSession(t, fixture, false)
	other := fixture.createSession(t)
	url := prepareRedis(t)
	first := newTestCoordinator(t, url, "capacityFirst")
	second := newTestCoordinator(t, url, "capacitySecond")
	manager := newRecoveryManager(t, fixture, first, realtime.RoomManagerOptions{MaxOwnedRooms: 1})
	if _, err := manager.EnsureRoom(fixture.ctx, rockets.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnsureRoom(fixture.ctx, other.SessionID); !errors.Is(err, realtime.ErrRealtimeCapacity) {
		t.Fatalf("room admission failed: %v", err)
	}
	if _, exists, err := first.CurrentRoomOwner(fixture.ctx, other.SessionID); err != nil || exists {
		t.Fatal("rejected activation retained lease")
	}
	remote := newRecoveryManager(t, fixture, second, realtime.RoomManagerOptions{MaxOwnedRooms: 1})
	if _, err := remote.EnsureRoom(fixture.ctx, other.SessionID); err != nil {
		t.Fatal(err)
	}
	ownership, err := manager.EnsureRoom(fixture.ctx, other.SessionID)
	if err != nil || ownership.Local {
		t.Fatalf("saturation rejected remote room: %+v %v", ownership, err)
	}
}
