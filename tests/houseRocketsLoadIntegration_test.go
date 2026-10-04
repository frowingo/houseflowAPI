package tests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	ws "github.com/fasthttp/websocket"
	"go.mongodb.org/mongo-driver/bson/primitive"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/internal/infrastructure/telemetry"
)

type loadWorkerProcess struct {
	url     string
	command *exec.Cmd
	stopped chan loadWorkerReport
	stderr  bytes.Buffer
	once    sync.Once
	final   loadWorkerReport
}

func startLoadWorker(t *testing.T, fixture *gameSessionApplicationFixture, index, rooms, connections int) *loadWorkerProcess {
	t.Helper()
	worker := &loadWorkerProcess{stopped: make(chan loadWorkerReport, 1)}
	worker.command = exec.Command(os.Args[0], "-test.run=^TestHouseRocketsLoadWorker$")
	worker.command.Env = append(os.Environ(), "HOUSEFLOW_LOAD_WORKER=true", "HOUSEFLOW_LOAD_DATABASE="+fixture.db.Name(), "HOUSEFLOW_LOAD_INSTANCE=loadWorker"+strconv.Itoa(index), "HOUSEFLOW_LOAD_ROOMS="+strconv.Itoa(rooms), "HOUSEFLOW_LOAD_CONNECTIONS="+strconv.Itoa(connections), "GOMAXPROCS=1", "GOMEMLIMIT=200MiB")
	stdout, err := worker.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	worker.command.Stderr = &worker.stderr
	if err := worker.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { worker.stop(t) })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "loadReady ") {
				ready <- strings.TrimPrefix(line, "loadReady ")
			}
			if strings.HasPrefix(line, "loadStopped ") {
				var report loadWorkerReport
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "loadStopped ")), &report) == nil {
					worker.stopped <- report
				}
			}
		}
	}()
	select {
	case worker.url = <-ready:
	case <-time.After(10 * time.Second):
		worker.stop(t)
		t.Fatal("load worker startup failed")
	}
	return worker
}

func (worker *loadWorkerProcess) stop(t *testing.T) loadWorkerReport {
	t.Helper()
	worker.once.Do(func() {
		_ = worker.command.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- worker.command.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("load worker exit: %v; stderr: %s", err, worker.stderr.String())
			}
		case <-time.After(12 * time.Second):
			_ = worker.command.Process.Kill()
			<-done
			t.Error("load worker required forced shutdown")
		}
		select {
		case worker.final = <-worker.stopped:
		case <-time.After(time.Second):
			t.Error("load worker did not report shutdown")
		}
		if worker.final.Realtime.ActiveRooms != 0 || worker.final.Realtime.ActiveConnections != 0 || worker.final.Realtime.PendingActivations != 0 || worker.final.Realtime.PendingConnections != 0 {
			t.Errorf("graceful shutdown retained rooms or sockets: %+v", worker.final.Realtime)
		}
	})
	return worker.final
}

func (worker *loadWorkerProcess) metrics(t *testing.T) loadWorkerReport {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(worker.url + "/loadMetrics")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var report loadWorkerReport
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	return report
}

type loadRoom struct {
	sessionID, houseID string
	players            []string
}

func prepareLoadRoom(t *testing.T, fixture *gameSessionApplicationFixture, players int) loadRoom {
	t.Helper()
	room := loadRoom{sessionID: primitive.NewObjectID().Hex(), houseID: primitive.NewObjectID().Hex()}
	now := time.Now().UTC()
	for index := 0; index < players; index++ {
		id := primitive.NewObjectID()
		room.players = append(room.players, id.Hex())
		if _, err := fixture.db.Collection("User").InsertOne(fixture.ctx, entities.User{Id: id, Firstname: "Load", Lastname: strconv.Itoa(index), Email: id.Hex() + "@test.invalid", IsActive: true, HouseIds: []string{room.houseID}, CreatedOn: now, UpdatedOn: now}); err != nil {
			t.Fatal(err)
		}
	}
	houseID, _ := primitive.ObjectIDFromHex(room.houseID)
	if _, err := fixture.db.Collection("House").InsertOne(fixture.ctx, entities.House{Id: houseID, OwnerId: room.players[0], MemberIds: room.players, MaxMemberCount: 8, Name: "Load house", Type: entities.SharedHouse, CreatedOn: now, UpdatedOn: now}); err != nil {
		t.Fatal(err)
	}
	rules := houseRockets.Definition().Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = time.Nanosecond, 0
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: room.sessionID, HouseID: room.houseID, GameKey: houseRockets.GameKey, ProtocolVersion: 2, Mode: gameDomain.RealtimeGame, Rules: rules, CreatedBy: room.players[0], CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range room.players {
		if err := session.JoinPlayer(id, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range room.players {
		if err := session.SetReady(id, true, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Advance(now.Add(time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.Create(fixture.ctx, session.Snapshot(), session.PendingEvents(), persistenceCommand("loadCreate"+room.sessionID, "create", "load")); err != nil {
		t.Fatal(err)
	}
	return room
}

type loadClientReport struct {
	Sent          uint64
	Acknowledged  uint64
	Snapshots     uint64
	ReceivedBytes uint64
	ACK           telemetry.DurationSummary
	Err           string
}

type loadClientReady struct {
	grant    houseRockets.HouseRocketsControlGrantedModel
	snapshot houseRockets.HouseRocketsSnapshotModel
}

func awaitLoadClientReady(connection *ws.Conn, playerID string) (loadClientReady, error) {
	var ready loadClientReady
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var message realtime.ServerMessage
		if err := connection.ReadJSON(&message); err != nil {
			return ready, fmt.Errorf("%w (phase=%s seq=%d grant=%t)", err, ready.snapshot.Phase, ready.snapshot.StateSequence, ready.grant.ControlGeneration != "")
		}
		switch message.Type {
		case houseRockets.ControlGrantedMessageType:
			if err := json.Unmarshal(message.Payload, &ready.grant); err != nil {
				return ready, err
			}
		case houseRockets.SnapshotMessageType:
			if err := json.Unmarshal(message.Payload, &ready.snapshot); err != nil {
				return ready, err
			}
		case realtime.CommandRejectedMessageType:
			return ready, fmt.Errorf("warmup input rejected")
		}
		if ready.grant.ControlGeneration != "" && ready.snapshot.Phase == houseRockets.PhasePlaying {
			for _, player := range ready.snapshot.Players {
				if player.PlayerID == playerID && player.ControlGeneration != nil && *player.ControlGeneration == ready.grant.ControlGeneration {
					_ = connection.SetReadDeadline(time.Time{})
					return ready, nil
				}
			}
		}
	}
}

// Synthetic, authenticated human clients only; this is not an online bot mode.
func runLoadClient(ctx context.Context, connection *ws.Conn, playerID string, ready loadClientReady) loadClientReport {
	defer connection.Close()
	type received struct {
		message realtime.ServerMessage
		bytes   int
		err     error
	}
	messages := make(chan received, 64)
	go func() {
		for {
			_, payload, err := connection.ReadMessage()
			message := realtime.ServerMessage{}
			if err == nil {
				err = json.Unmarshal(payload, &message)
			}
			select {
			case messages <- received{message, len(payload), err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	var report loadClientReport
	var latency telemetry.DurationHistogram
	pending := make(map[int64]time.Time)
	grant, latest := ready.grant, ready.snapshot
	var sequence, lastACK int64
	lastSnapshotSequence := latest.StateSequence
	ticker := time.NewTicker(time.Second / houseRockets.MaximumInputRateHz)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			report.ACK = latency.Snapshot()
			return report
		case received := <-messages:
			if received.err != nil {
				report.Err = "socket read failed"
				return report
			}
			report.ReceivedBytes += uint64(received.bytes)
			switch received.message.Type {
			case realtime.CommandRejectedMessageType:
				report.Err = "server rejected input"
				return report
			case houseRockets.ControlGrantedMessageType:
				if err := json.Unmarshal(received.message.Payload, &grant); err != nil {
					report.Err = "invalid grant"
					return report
				}
			case houseRockets.SnapshotMessageType:
				if err := json.Unmarshal(received.message.Payload, &latest); err != nil {
					report.Err = "invalid snapshot"
					return report
				}
				if latest.StateSequence <= lastSnapshotSequence {
					report.Err = "snapshot sequence regressed"
					return report
				}
				lastSnapshotSequence = latest.StateSequence
				report.Snapshots++
				if latest.Phase == houseRockets.PhaseCancelled || latest.Phase == houseRockets.PhaseFinalizing {
					report.Err = "match terminated before load window finished"
					return report
				}
				for _, player := range latest.Players {
					if player.PlayerID != playerID {
						continue
					}
					ack := player.LastProcessedInputSequence
					if ack > lastACK {
						if sent, exists := pending[ack]; exists {
							latency.Observe(time.Since(sent))
							report.Acknowledged++
						}
						for value := range pending {
							if value <= ack {
								delete(pending, value)
							}
						}
						lastACK = ack
					}
				}
			}
		case <-ticker.C:
			if grant.ControlGeneration == "" || latest.Phase != houseRockets.PhasePlaying {
				continue
			}
			if len(pending) >= 64 {
				report.Err = "ACK backlog reached bound"
				return report
			}
			heading := 0.0
			for _, player := range latest.Players {
				if player.PlayerID != playerID {
					continue
				}
				if !player.IsAlive {
					report.Err = "player eliminated before load window finished"
					return report
				}
				targetY, deltaX := 180.0, 150.0
			findTarget:
				for _, gate := range latest.Gates {
					for _, section := range gate.Sections {
						if x := gate.WorldX + section.OffsetX; x > player.WorldX+70 {
							targetY = (section.LowerY + section.UpperY) / 2
							deltaX = x - player.WorldX
							break findTarget
						}
					}
				}
				heading = math.Atan2(targetY-player.WorldY, deltaX)
			}
			sequence++
			payload, _ := json.Marshal(houseRockets.HouseRocketsSteerModel{ControlGeneration: grant.ControlGeneration, InputSequence: sequence, Heading: heading})
			pending[sequence] = time.Now()
			_ = connection.SetWriteDeadline(time.Now().Add(time.Second))
			if err := connection.WriteJSON(realtime.ClientMessage{ProtocolVersion: 2, MessageID: "loadInput" + strconv.FormatInt(sequence, 10), Type: houseRockets.SteerMessageType, Payload: payload}); err != nil {
				report.Err = "socket write failed"
				return report
			}
			report.Sent++
		}
	}
}

func runHouseRocketsLoad(t *testing.T, roomCount, playerCount int, duration time.Duration) {
	t.Helper()
	fixture := newGameSessionApplicationFixture(t)
	prepareRedis(t) // worker inherits the test-only URL, never a production secret.
	rooms := make([]loadRoom, roomCount)
	for index := range rooms {
		rooms[index] = prepareLoadRoom(t, fixture, playerCount)
	}
	workers := []*loadWorkerProcess{startLoadWorker(t, fixture, 0, roomCount, roomCount*playerCount), startLoadWorker(t, fixture, 1, roomCount, roomCount*playerCount)}
	jwt := helpers.NewJWTService("loadTestSecret")
	type client struct {
		connection *ws.Conn
		playerID   string
		ready      loadClientReady
	}
	var clients []client
	for roomIndex, room := range rooms {
		for playerIndex, id := range room.players {
			worker := workers[(roomIndex+playerIndex)%2]
			if playerIndex == 0 {
				request, _ := http.NewRequest(http.MethodPut, worker.url+"/api/v1/game/houseRockets/session", strings.NewReader(fmt.Sprintf(`{"houseId":%q}`, room.houseID)))
				request.Header.Set("Authorization", "Bearer "+generateGatewayToken(t, jwt, id))
				request.Header.Set("Content-Type", "application/json")
				response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
				if err != nil {
					t.Fatal(err)
				}
				var body struct {
					Data struct {
						SessionID string `json:"sessionId"`
					} `json:"data"`
				}
				_ = json.NewDecoder(response.Body).Decode(&body)
				response.Body.Close()
				if response.StatusCode != 200 || body.Data.SessionID != room.sessionID {
					t.Fatal("HTTP discovery did not reuse unique active room")
				}
			}
			connection, _, err := ws.DefaultDialer.Dial("ws"+worker.url[4:]+"/api/v1/game/"+room.sessionID+"/realtime?protocolVersion=2", http.Header{"Authorization": []string{"Bearer " + generateGatewayToken(t, jwt, id)}})
			if err != nil {
				t.Fatal(err)
			}
			clients = append(clients, client{connection: connection, playerID: id})
		}
	}
	defer func() {
		for _, client := range clients {
			client.connection.Close()
		}
	}()
	var warmup sync.WaitGroup
	for index := range clients {
		warmup.Add(1)
		go func() {
			defer warmup.Done()
			ready, err := awaitLoadClientReady(clients[index].connection, clients[index].playerID)
			if err != nil {
				t.Errorf("load warmup: %v", err)
				return
			}
			clients[index].ready = ready
		}()
	}
	warmup.Wait()
	if t.Failed() {
		for _, worker := range workers {
			t.Logf("warmup metrics: %s", mustJSON(t, worker.metrics(t)))
		}
		return
	}
	before := []loadWorkerReport{workers[0].metrics(t), workers[1].metrics(t)}
	if before[0].Realtime.ActiveRooms+before[1].Realtime.ActiveRooms != roomCount || before[0].Realtime.ActiveConnections+before[1].Realtime.ActiveConnections != len(clients) {
		t.Fatal("measurement did not start with all rooms and clients active")
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	results := make(chan loadClientReport, len(clients))
	for _, client := range clients {
		go func() { results <- runLoadClient(ctx, client.connection, client.playerID, client.ready) }()
	}
	var clientResults []loadClientReport
	for range clients {
		result := <-results
		clientResults = append(clientResults, result)
		if result.Err != "" {
			t.Error(result.Err)
		}
		if result.ACK.Count == 0 || result.Snapshots == 0 {
			t.Error("client received no applied input/fanout")
		}
	}
	after := []loadWorkerReport{workers[0].metrics(t), workers[1].metrics(t)}
	for index, report := range after {
		if report.Realtime.RuntimeOverloads != 0 || report.Realtime.CheckpointFailures != 0 || report.Realtime.SlowConsumers != 0 || report.Realtime.AdmissionRejected != 0 || report.Realtime.Errors != 0 {
			t.Error("load violated runtime safety bounds")
		}
		if report.MaxRSSBytes > 256*1024*1024 {
			t.Error("worker exceeded reference 256 MiB RSS budget")
		}
		cpuDelta := report.CPUSeconds - before[index].CPUSeconds
		t.Logf("LOAD_WORKER rooms=%d players=%d worker=%d cpuCorePercent=%.2f redisClientCommandsPerSecond=%.2f socketPayloadBytesPerSecond=%.2f checkpointWritesPerSecond=%.2f metrics=%s", roomCount, playerCount, index, 100*cpuDelta/duration.Seconds(), float64(report.Redis.ClientCommands-before[index].Redis.ClientCommands)/duration.Seconds(), float64(report.Realtime.SocketWrittenPayloadBytes-before[index].Realtime.SocketWrittenPayloadBytes)/duration.Seconds(), float64(report.Realtime.CheckpointWrites-before[index].Realtime.CheckpointWrites)/duration.Seconds(), mustJSON(t, report))
	}
	var maxACK float64
	var worstP95 float64
	var bytesReceived, sent, acknowledged uint64
	for _, report := range clientResults {
		maxACK = math.Max(maxACK, report.ACK.MaxMilliseconds)
		worstP95 = math.Max(worstP95, report.ACK.P95UpperMilliseconds)
		bytesReceived += report.ReceivedBytes
		sent += report.Sent
		acknowledged += report.Acknowledged
	}
	t.Logf("LOAD_CLIENT rooms=%d players=%d durationSeconds=%.2f sent=%d acknowledgedSamples=%d maxAckMilliseconds=%.2f worstConnectionP95UpperMilliseconds=%.2f receivedBytesPerSecond=%.2f generatorPlatform=%s/%s", roomCount, playerCount, duration.Seconds(), sent, acknowledged, maxACK, worstP95, float64(bytesReceived)/duration.Seconds(), runtime.GOOS, runtime.GOARCH)
	for _, worker := range workers {
		worker.stop(t)
	}
}

// Four rooms force HTTP request-buffer reuse on both gateways. Retained route
// parameters must not change an earlier room's lease ID or prevent its cleanup.
func TestHouseRocketsMultiRoomGatewaySmoke(t *testing.T) { runHouseRocketsLoad(t, 4, 2, 2*time.Second) }

func TestHouseRocketsLoadMatrix(t *testing.T) {
	if os.Getenv("HOUSEFLOW_REALTIME_LOAD") != "true" {
		t.Skip("opt-in load measurement")
	}
	duration := 3 * time.Second
	if raw := os.Getenv("HOUSEFLOW_LOAD_DURATION"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < 2*time.Second || parsed > 20*time.Second {
			t.Fatal("HOUSEFLOW_LOAD_DURATION must be between 2s and 20s")
		}
		duration = parsed
	}
	for _, rooms := range []int{1, 4, 8} {
		for _, players := range []int{2, 4, 8} {
			t.Run(fmt.Sprintf("rooms%dPlayers%d", rooms, players), func(t *testing.T) { runHouseRocketsLoad(t, rooms, players, duration) })
		}
	}
}
