package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	gameApplication "houseflowApi/internal/application/game"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	rocketsCommands "houseflowApi/internal/application/game/gameSpesific/houseRockets/commands"
	rocketsQueries "houseflowApi/internal/application/game/gameSpesific/houseRockets/queries"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/controllers"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/coordination"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/middleware"
	"houseflowApi/internal/infrastructure/realtime"
)

type loadWorkerReport struct {
	Realtime       realtime.MetricsSnapshot          `json:"realtime"`
	Redis          coordination.RedisMetricsSnapshot `json:"redis"`
	CPUSeconds     float64                           `json:"cpuSeconds"`
	MaxRSSBytes    uint64                            `json:"maxRssBytes"`
	GoHeapBytes    uint64                            `json:"goHeapBytes"`
	GoRuntimeBytes uint64                            `json:"goRuntimeBytes"`
	Goroutines     int                               `json:"goroutines"`
	GoMaxProcs     int                               `json:"goMaxProcs"`
	Platform       string                            `json:"platform"`
}

func loadWorkerMeasurements(service *realtime.Service, coordinator *coordination.RedisCoordinator) loadWorkerReport {
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	rss := uint64(usage.Maxrss)
	if runtime.GOOS != "darwin" {
		rss *= 1024
	}
	return loadWorkerReport{Realtime: service.Metrics(), Redis: coordinator.Metrics(), CPUSeconds: float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6, MaxRSSBytes: rss, GoHeapBytes: memory.HeapAlloc, GoRuntimeBytes: memory.Sys, Goroutines: runtime.NumGoroutine(), GoMaxProcs: runtime.GOMAXPROCS(0), Platform: runtime.GOOS + "/" + runtime.GOARCH}
}

// Only spawned by the load harness, against a disposable fixture database and
// loopback listener. No diagnostics endpoint is added to the production API.
func TestHouseRocketsLoadWorker(t *testing.T) {
	if os.Getenv("HOUSEFLOW_LOAD_WORKER") != "true" {
		t.Skip("load subprocess helper")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(os.Getenv("HOUSEFLOW_TEST_MONGO_URI")))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	db := client.Database(os.Getenv("HOUSEFLOW_LOAD_DATABASE"))
	coordinator, err := coordination.NewRedisCoordinator(coordination.RedisOptions{URL: os.Getenv("HOUSEFLOW_TEST_REDIS_URL"), InstanceID: os.Getenv("HOUSEFLOW_LOAD_INSTANCE")})
	if err != nil {
		t.Fatal(err)
	}
	coordinator.Start(ctx)
	repository := database.NewGameSessionRepository(client, db.Name())
	fixture := &gameSessionApplicationFixture{gameSessionPersistenceFixture: &gameSessionPersistenceFixture{ctx: ctx, db: db, repository: repository}}
	mediator := newGameSessionMediator(fixture)
	matches := database.NewGameMatchRepository(client, db.Name())
	catalog, err := gameApplication.NewCatalog(houseRockets.Definition())
	if err != nil {
		t.Fatal(err)
	}
	cqrs.MustRegister[houseRockets.HouseRocketsResultModel, rocketsCommands.CompleteMatchCommand](mediator, rocketsCommands.NewCompleteMatchHandler(repository, matches))
	cqrs.MustRegister[houseRockets.HouseRocketsResultModel, rocketsQueries.GetMatchResultQuery](mediator, rocketsQueries.NewGetMatchResultHandler(repository, matches, fixture.membershipPolicy()))
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameQueries.AuthorizeCancellationQuery](mediator, gameQueries.NewAuthorizeCancellationHandler(repository, fixture.membershipPolicy()))
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.EnsureActiveGameSessionCommand](mediator, gameCommands.NewEnsureActiveGameSessionHandler(repository, catalog, fixture.membershipPolicy()))
	maxRooms, _ := strconv.Atoi(os.Getenv("HOUSEFLOW_LOAD_ROOMS"))
	maxConnections, _ := strconv.Atoi(os.Getenv("HOUSEFLOW_LOAD_CONNECTIONS"))
	service, err := realtime.NewService(coordinator, repository, mediator, realtime.RoomManagerOptions{ParticipantDirectory: database.NewGameParticipantDirectory(client, db.Name()), MatchRepository: matches, MaxOwnedRooms: maxRooms}, realtime.GatewayOptions{EnableHouseRockets: true, MaxConnections: maxConnections, AllowedOrigins: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	jwt := helpers.NewJWTService("loadTestSecret")
	routes := app.Group("/api/v1/game", middleware.AuthRequired(jwt))
	controller := controllers.NewGameController(mediator, nil)
	routes.Put("/:gameKey/session", controller.EnsureActiveSession)
	routes.Get("/:sessionId/result", controller.GetResult)
	routes.Get("/:sessionId/realtime", service.UpgradeMiddleware(), service.Handler())
	app.Get("/loadMetrics", func(c *fiber.Ctx) error { return c.JSON(loadWorkerMeasurements(service, coordinator)) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(listener) }()
	fmt.Println("loadReady http://" + listener.Addr().String())
	// Drain bounded error queues without logging credentials/room identifiers.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-service.Errors():
			case <-service.RoomErrors():
			}
		}
	}()
	<-ctx.Done()
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := service.Close(shutdown); err != nil {
		t.Error(err)
	}
	if err := app.ShutdownWithContext(shutdown); err != nil {
		t.Error(err)
	}
	if err := coordinator.Close(shutdown); err != nil {
		t.Error(err)
	}
	result, _ := json.Marshal(loadWorkerMeasurements(service, coordinator))
	fmt.Println("loadStopped " + string(result))
}
