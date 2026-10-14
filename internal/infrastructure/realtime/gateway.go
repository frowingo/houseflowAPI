package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	fasthttpWebsocket "github.com/fasthttp/websocket"
	fiberWebsocket "github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const (
	defaultMaximumMessageBytes  = 16 * 1024
	defaultOutboundQueueSize    = 64
	defaultPingInterval         = 15 * time.Second
	defaultIdleTimeout          = 45 * time.Second
	defaultWriteTimeout         = 5 * time.Second
	defaultPresenceTTL          = 30 * time.Second
	defaultPresenceRefresh      = 10 * time.Second
	defaultMessageRateWindow    = 10 * time.Second
	defaultMaximumMessages      = 30
	defaultCommandDispatchLimit = 3 * time.Second
	defaultUpgradeTimeout       = 5 * time.Second
	connectionCleanupTimeout    = 2 * time.Second
)

var (
	ErrGatewayNotStarted = errors.New("realtime gateway is not started")
	ErrGatewayStopped    = errors.New("realtime gateway is stopped")
)

type GatewayOptions struct {
	MaximumMessageBytes int64
	OutboundQueueSize   int
	PingInterval        time.Duration
	IdleTimeout         time.Duration
	WriteTimeout        time.Duration
	PresenceTTL         time.Duration
	PresenceRefresh     time.Duration
	MessageRateWindow   time.Duration
	MaximumMessages     int
	CommandTimeout      time.Duration
	UpgradeTimeout      time.Duration
	AllowedOrigins      []string
	Localizer           helpers.MessageLocalizer
	MaxConnections      int
	Metrics             *Metrics
}

type Gateway struct {
	coordinator coordinationAbstract.Coordinator
	manager     *RoomManager
	sender      cqrs.Sender
	options     GatewayOptions

	mutex        sync.Mutex
	hubs         map[string]*roomHub
	clients      map[string]*gatewayClient
	reservations map[*connectionReservation]struct{}
	started      bool
	closed       bool
	ctx          context.Context
	cancel       context.CancelFunc
	errors       chan RuntimeError
	workers      sync.WaitGroup
}

func NewGateway(
	coordinator coordinationAbstract.Coordinator,
	manager *RoomManager,
	sender cqrs.Sender,
	options GatewayOptions,
) (*Gateway, error) {
	if coordinator == nil {
		return nil, errors.New("realtime coordinator is required")
	}
	if manager == nil {
		return nil, errors.New("room manager is required")
	}
	if sender == nil {
		return nil, errors.New("CQRS sender is required")
	}
	options = normalizeGatewayOptions(options)
	if options.Metrics == nil {
		options.Metrics = manager.options.Metrics
	}
	if options.MaxConnections < 0 {
		return nil, errors.New("maximum connections must not be negative")
	}
	if manager.options.Games.HasRealtimeGames() && manager.options.ParticipantDirectory == nil {
		return nil, errors.New("realtime games require a participant directory")
	}
	if options.PingInterval >= options.IdleTimeout {
		return nil, errors.New("ping interval must be shorter than idle timeout")
	}
	if options.PresenceRefresh >= options.PresenceTTL {
		return nil, errors.New("presence refresh interval must be shorter than presence TTL")
	}
	gateway := &Gateway{
		coordinator:  coordinator,
		manager:      manager,
		sender:       sender,
		options:      options,
		hubs:         make(map[string]*roomHub),
		clients:      make(map[string]*gatewayClient),
		reservations: make(map[*connectionReservation]struct{}),
		errors:       make(chan RuntimeError, 32),
	}
	manager.attachAudience(gateway)
	return gateway, nil
}

// deliverLocal hands an event published by a room this instance owns to the
// room's local sockets; the Redis copy of the same event is ignored.
func (gateway *Gateway) deliverLocal(event coordinationAbstract.MessageEnvelope) {
	gateway.mutex.Lock()
	hub := gateway.hubs[event.RoomID]
	gateway.mutex.Unlock()
	if hub != nil {
		hub.broadcastEvent(event)
	}
}

func (gateway *Gateway) hasConnection(roomID, connectionID string) bool {
	gateway.mutex.Lock()
	hub := gateway.hubs[roomID]
	gateway.mutex.Unlock()
	if hub == nil {
		return false
	}
	hub.mutex.RLock()
	defer hub.mutex.RUnlock()
	_, exists := hub.clients[connectionID]
	return exists
}

func (gateway *Gateway) Start(ctx context.Context) error {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	if gateway.closed {
		return ErrGatewayStopped
	}
	if gateway.started {
		return nil
	}
	gateway.ctx, gateway.cancel = context.WithCancel(ctx)
	gateway.started = true
	return nil
}

func (gateway *Gateway) Errors() <-chan RuntimeError {
	return gateway.errors
}

func (gateway *Gateway) UpgradeMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !fiberWebsocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}
		if !gateway.originAllowed(c.Get(fiber.HeaderOrigin)) {
			return c.SendStatus(fiber.StatusForbidden)
		}
		if err := gateway.running(); err != nil {
			return helpers.RespondLocalizedError(
				c,
				gateway.options.Localizer,
				helpers.NewUnavailableError(roomUnavailableErrorCode, err),
			)
		}
		// Fiber params borrow the HTTP request buffer. Room ownership outlives
		// this handler, so retain an owned copy before starting the runtime.
		roomID := strings.Clone(strings.TrimSpace(c.Params("sessionId")))
		userID, _ := c.Locals("userID").(string)
		if roomID == "" || userID == "" {
			return helpers.RespondLocalizedError(
				c,
				gateway.options.Localizer,
				helpers.NewLocalizedError(defaultInvalidCommandErrorCode),
			)
		}
		requestCtx, cancel := context.WithTimeout(c.UserContext(), gateway.options.UpgradeTimeout)
		defer cancel()
		snapshot, err := cqrs.Send[gameDomain.SessionSnapshot](requestCtx, gateway.sender, gameQueries.GetGameSessionQuery{
			SessionID: roomID,
			UserID:    userID,
		})
		if err != nil {
			return helpers.RespondLocalizedError(c, gateway.options.Localizer, err)
		}
		version, _ := strconv.Atoi(c.Query("protocolVersion"))
		if version != V2ProtocolVersion || snapshot.ProtocolVersion != V2ProtocolVersion {
			return helpers.RespondLocalizedError(c, gateway.options.Localizer, helpers.NewLocalizedError(unsupportedProtocolErrorCode))
		}
		game := gateway.manager.realtimeGame(snapshot.GameKey)
		if game == nil {
			return helpers.RespondLocalizedError(c, gateway.options.Localizer, helpers.NewNotFoundError("game.error.definition_not_found"))
		}
		if err := gateway.requireActiveMember(requestCtx, snapshot.HouseID, userID); err != nil {
			return helpers.RespondLocalizedError(c, gateway.options.Localizer, err)
		}
		reservation, err := gateway.reserveConnection()
		if err != nil {
			c.Set(fiber.HeaderRetryAfter, "1")
			return helpers.RespondLocalizedError(c, gateway.options.Localizer, helpers.NewUnavailableError(roomUnavailableErrorCode, err))
		}
		ownership, err := gateway.manager.EnsureRoom(requestCtx, roomID)
		if err != nil {
			reservation.release()
			gateway.report(roomID, err)
			if errors.Is(err, ErrRealtimeCapacity) {
				c.Set(fiber.HeaderRetryAfter, "1")
			}
			if errors.Is(err, ErrTerminalRoom) {
				err = helpers.NewConflictError("game.error.invalid_state")
			} else {
				err = helpers.NewUnavailableError(roomUnavailableErrorCode, err)
			}
			return helpers.RespondLocalizedError(c, gateway.options.Localizer, err)
		}
		c.Locals("realtimeEpoch", ownership.RuntimeEpoch)
		c.Locals("realtimeGame", game)
		c.Locals("realtimeSettings", snapshot.Settings)
		c.Locals("realtimeReservation", reservation)
		if err := c.Next(); err != nil {
			reservation.release()
			return err
		}
		return nil
	}
}

func (gateway *Gateway) Handler() fiber.Handler {
	return fiberWebsocket.New(gateway.handleConnection, fiberWebsocket.Config{
		HandshakeTimeout: gateway.options.UpgradeTimeout,
		Origins:          []string{"*"},
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
	})
}

func (gateway *Gateway) Close(ctx context.Context) error {
	gateway.mutex.Lock()
	if gateway.closed {
		gateway.mutex.Unlock()
		return nil
	}
	gateway.closed = true
	gateway.started = false
	for reservation := range gateway.reservations {
		reservation.releaseLocked()
	}
	cancel := gateway.cancel
	clients := make([]*gatewayClient, 0, len(gateway.clients))
	for _, client := range gateway.clients {
		clients = append(clients, client)
	}
	hubs := make([]*roomHub, 0, len(gateway.hubs))
	for _, hub := range gateway.hubs {
		hubs = append(hubs, hub)
	}
	gateway.mutex.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, client := range clients {
		client.stop(fasthttpWebsocket.CloseGoingAway, "server shutdown")
	}
	for _, hub := range hubs {
		hub.stop()
	}

	done := make(chan struct{})
	go func() {
		gateway.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (gateway *Gateway) handleConnection(socket *fiberWebsocket.Conn) {
	roomID := strings.Clone(socket.Params("sessionId"))
	userID, _ := socket.Locals("userID").(string)
	connection := newGatewayClient(gateway, socket, roomID, userID)
	if !gateway.registerClient(connection) {
		connection.stop(fasthttpWebsocket.CloseTryAgainLater, "realtime gateway unavailable")
		return
	}
	var hub *roomHub
	presenceActive := false
	writerStarted := false
	defer func() {
		connection.disconnectController()
		connection.stop(fasthttpWebsocket.CloseNormalClosure, "connection closed")
		if writerStarted {
			<-connection.writerDone
		}
		if presenceActive {
			gateway.removePresence(roomID, connection.id)
		}
		if hub != nil {
			gateway.leaveHub(hub, connection)
		}
		gateway.unregisterClient(connection)
		gateway.workers.Done()
	}()

	var err error
	hub, err = gateway.joinHub(connection)
	if err != nil {
		gateway.report(roomID, err)
		connection.stop(fasthttpWebsocket.CloseTryAgainLater, "room subscription unavailable")
		return
	}

	presence := coordinationAbstract.Presence{
		RoomID:       roomID,
		UserID:       userID,
		ConnectionID: connection.id,
	}
	presenceCtx, presenceCancel := context.WithTimeout(gateway.ctx, gateway.options.WriteTimeout)
	err = gateway.coordinator.TouchPresence(presenceCtx, presence, gateway.options.PresenceTTL)
	presenceCancel()
	if err != nil {
		gateway.report(roomID, err)
		connection.stop(fasthttpWebsocket.CloseTryAgainLater, "presence unavailable")
		return
	}
	presenceActive = true

	snapshotCtx, snapshotCancel := context.WithTimeout(gateway.ctx, gateway.options.UpgradeTimeout)
	snapshot, err := cqrs.Send[gameDomain.SessionSnapshot](snapshotCtx, gateway.sender, gameQueries.GetGameSessionQuery{
		SessionID: roomID,
		UserID:    userID,
	})
	snapshotCancel()
	if err != nil {
		gateway.report(roomID, err)
		connection.stop(fasthttpWebsocket.ClosePolicyViolation, "session unavailable")
		return
	}
	if !connection.activateV2(snapshot) {
		connection.stop(fasthttpWebsocket.ClosePolicyViolation, "outbound queue unavailable")
		return
	}

	if !gateway.startWorker() {
		return
	}
	writerStarted = true
	go connection.writeLoop()
	if gateway.startWorker() {
		go connection.controlLoop(presence)
	}
	connection.readLoop()
}

func (gateway *Gateway) startWorker() bool {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	if gateway.closed || !gateway.started {
		return false
	}
	gateway.workers.Add(1)
	return true
}

func (gateway *Gateway) registerClient(client *gatewayClient) bool {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	if gateway.closed || !gateway.started {
		return false
	}
	if reservation, ok := client.socket.Locals("realtimeReservation").(*connectionReservation); ok {
		if reservation.gateway != gateway || reservation.released {
			return false
		}
		reservation.releaseLocked()
	} else if gateway.options.MaxConnections > 0 && len(gateway.clients)+len(gateway.reservations) >= gateway.options.MaxConnections {
		gateway.options.Metrics.admissionRejected.Add(1)
		return false
	}
	gateway.clients[client.id] = client
	gateway.workers.Add(1)
	return true
}

func (gateway *Gateway) unregisterClient(client *gatewayClient) {
	gateway.mutex.Lock()
	delete(gateway.clients, client.id)
	gateway.mutex.Unlock()
}

func (gateway *Gateway) joinHub(client *gatewayClient) (*roomHub, error) {
	gateway.mutex.Lock()
	if gateway.closed || !gateway.started {
		gateway.mutex.Unlock()
		return nil, ErrGatewayStopped
	}
	if existing := gateway.hubs[client.roomID]; existing != nil {
		existing.add(client)
		gateway.mutex.Unlock()
		return existing, nil
	}
	gateway.mutex.Unlock()

	subscription, err := gateway.coordinator.SubscribeRoomEvents(gateway.ctx, client.roomID)
	if err != nil {
		return nil, err
	}
	hubCtx, hubCancel := context.WithCancel(gateway.ctx)
	candidate := &roomHub{
		gateway:      gateway,
		roomID:       client.roomID,
		ctx:          hubCtx,
		cancel:       hubCancel,
		subscription: subscription,
		clients:      make(map[string]*gatewayClient),
	}

	gateway.mutex.Lock()
	if gateway.closed || !gateway.started {
		gateway.mutex.Unlock()
		candidate.stop()
		return nil, ErrGatewayStopped
	}
	if existing := gateway.hubs[client.roomID]; existing != nil {
		existing.add(client)
		gateway.mutex.Unlock()
		candidate.stop()
		return existing, nil
	}
	candidate.add(client)
	gateway.hubs[client.roomID] = candidate
	gateway.workers.Add(1)
	gateway.mutex.Unlock()
	go candidate.run()
	return candidate, nil
}

func (gateway *Gateway) leaveHub(hub *roomHub, client *gatewayClient) {
	gateway.mutex.Lock()
	hub.mutex.Lock()
	delete(hub.clients, client.id)
	empty := len(hub.clients) == 0
	hub.mutex.Unlock()
	if empty && gateway.hubs[hub.roomID] == hub {
		delete(gateway.hubs, hub.roomID)
	}
	gateway.mutex.Unlock()
	if empty {
		hub.stop()
	}
}

func (gateway *Gateway) removePresence(roomID string, connectionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), connectionCleanupTimeout)
	defer cancel()
	if err := gateway.coordinator.RemovePresence(ctx, roomID, connectionID); err != nil {
		gateway.report(roomID, err)
	}
}

func (gateway *Gateway) running() error {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	if gateway.closed {
		return ErrGatewayStopped
	}
	if !gateway.started || gateway.ctx == nil {
		return ErrGatewayNotStarted
	}
	return nil
}

func (gateway *Gateway) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	for _, allowed := range gateway.options.AllowedOrigins {
		if allowed == "*" || strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

func (gateway *Gateway) report(roomID string, err error) {
	gateway.options.Metrics.errors.Add(1)
	select {
	case gateway.errors <- RuntimeError{RoomID: roomID, Err: err}:
	default:
	}
}

func normalizeGatewayOptions(options GatewayOptions) GatewayOptions {
	if options.MaximumMessageBytes <= 0 {
		options.MaximumMessageBytes = defaultMaximumMessageBytes
	}
	if options.OutboundQueueSize <= 0 {
		options.OutboundQueueSize = defaultOutboundQueueSize
	}
	if options.PingInterval <= 0 {
		options.PingInterval = defaultPingInterval
	}
	if options.IdleTimeout <= 0 {
		options.IdleTimeout = defaultIdleTimeout
	}
	if options.WriteTimeout <= 0 {
		options.WriteTimeout = defaultWriteTimeout
	}
	if options.PresenceTTL <= 0 {
		options.PresenceTTL = defaultPresenceTTL
	}
	if options.PresenceRefresh <= 0 {
		options.PresenceRefresh = defaultPresenceRefresh
	}
	if options.MessageRateWindow <= 0 {
		options.MessageRateWindow = defaultMessageRateWindow
	}
	if options.MaximumMessages <= 0 {
		options.MaximumMessages = defaultMaximumMessages
	}
	if options.CommandTimeout <= 0 {
		options.CommandTimeout = defaultCommandDispatchLimit
	}
	if options.UpgradeTimeout <= 0 {
		options.UpgradeTimeout = defaultUpgradeTimeout
	}
	return options
}

type gatewayClient struct {
	v2         *v2ClientState
	expiresAt  time.Time
	id         string
	roomID     string
	userID     string
	gateway    *Gateway
	socket     *fiberWebsocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	outbound   chan []byte
	mutex      sync.Mutex
	active     bool
	stopOnce   sync.Once
	rateLimit  connectionRateLimit
	violations int
	writerDone chan struct{}
}

func newGatewayClient(
	gateway *Gateway,
	socket *fiberWebsocket.Conn,
	roomID string,
	userID string,
) *gatewayClient {
	ctx, cancel := context.WithCancel(gateway.ctx)
	client := &gatewayClient{
		id:         uuid.NewString(),
		roomID:     roomID,
		userID:     userID,
		gateway:    gateway,
		socket:     socket,
		ctx:        ctx,
		cancel:     cancel,
		outbound:   make(chan []byte, gateway.options.OutboundQueueSize),
		writerDone: make(chan struct{}),
		rateLimit: connectionRateLimit{
			window:  gateway.options.MessageRateWindow,
			maximum: gateway.options.MaximumMessages,
		},
	}
	client.expiresAt, _ = socket.Locals("tokenExpiresAt").(time.Time)
	epoch, _ := socket.Locals("realtimeEpoch").(int64)
	game, _ := socket.Locals("realtimeGame").(module.Game)
	settings, _ := socket.Locals("realtimeSettings").(json.RawMessage)
	client.v2 = newV2ClientState(game, settings, epoch)
	return client
}

func (client *gatewayClient) readLoop() {
	client.socket.SetReadLimit(client.gateway.options.MaximumMessageBytes)
	_ = client.socket.SetReadDeadline(time.Now().Add(client.gateway.options.IdleTimeout))
	client.socket.SetPongHandler(func(string) error {
		return client.socket.SetReadDeadline(time.Now().Add(client.gateway.options.IdleTimeout))
	})
	client.readV2Loop()
}

func (client *gatewayClient) recordViolation() bool {
	client.violations++
	if client.violations < 3 {
		return false
	}
	client.stop(fasthttpWebsocket.ClosePolicyViolation, "too many protocol violations")
	return true
}

// Presence is refreshed by controlLoop; this loop only owns socket writes.
func (client *gatewayClient) writeLoop() {
	defer client.gateway.workers.Done()
	defer close(client.writerDone)
	pingTicker := time.NewTicker(client.gateway.options.PingInterval)
	defer pingTicker.Stop()
	var expiration <-chan time.Time
	if !client.expiresAt.IsZero() {
		timer := time.NewTimer(time.Until(client.expiresAt))
		defer timer.Stop()
		expiration = timer.C
	}
	write := func(payload []byte) bool {
		started := time.Now()
		_ = client.socket.SetWriteDeadline(time.Now().Add(client.gateway.options.WriteTimeout))
		err := client.socket.WriteMessage(fasthttpWebsocket.TextMessage, payload)
		client.gateway.options.Metrics.observeSocketWrite(started, len(payload), err)
		if err != nil {
			client.stop(fasthttpWebsocket.CloseAbnormalClosure, "write failed")
			return false
		}
		return true
	}
	for {
		// Control/session messages are ordered ahead of coalesced motion frames.
		select {
		case payload := <-client.outbound:
			if !write(payload) {
				return
			}
			continue
		default:
		}
		select {
		case <-expiration:
			client.stop(fasthttpWebsocket.ClosePolicyViolation, "authentication expired")
			return
		case payload := <-client.v2.snapshots:
			if !write(payload) {
				return
			}
		case <-client.ctx.Done():
			client.stop(fasthttpWebsocket.CloseGoingAway, "realtime gateway stopping")
			return
		case payload := <-client.outbound:
			if !write(payload) {
				return
			}
		case <-pingTicker.C:
			deadline := time.Now().Add(client.gateway.options.WriteTimeout)
			if err := client.socket.WriteControl(fasthttpWebsocket.PingMessage, nil, deadline); err != nil {
				client.stop(fasthttpWebsocket.CloseAbnormalClosure, "ping failed")
				return
			}
		}
	}
}

func (client *gatewayClient) reject(messageID string, protocolError ProtocolError) {
	client.enqueueV2(NewV2ErrorMessage(messageID, time.Now(), V2ProtocolError{Code: protocolError.Code, Args: protocolError.Args, Retryable: protocolError.Retryable}), "", 0)
}

// The close code documents intent at call sites; no close frame is written.
func (client *gatewayClient) stop(_ int, reason string) {
	client.stopOnce.Do(func() {
		if reason == "slow consumer" {
			client.gateway.options.Metrics.slowConsumers.Add(1)
		}
		client.cancel()
		// fasthttp's hijacked Conn.Close may defer closing until the handler
		// returns. A net.Conn deadline interrupts the concurrent reader/writer
		// immediately, without a blocking close-frame write in room fanout.
		_ = client.socket.NetConn().SetDeadline(time.Now())
		_ = client.socket.Close()
	})
}

type connectionRateLimit struct {
	window      time.Duration
	maximum     int
	windowStart time.Time
	count       int
}

func (limit *connectionRateLimit) allow(now time.Time) bool {
	if limit.windowStart.IsZero() || now.Sub(limit.windowStart) >= limit.window {
		limit.windowStart = now
		limit.count = 0
	}
	limit.count++
	return limit.count <= limit.maximum
}

type roomHub struct {
	gateway      *Gateway
	roomID       string
	ctx          context.Context
	cancel       context.CancelFunc
	subscription coordinationAbstract.RoomEventSubscription
	mutex        sync.RWMutex
	clients      map[string]*gatewayClient
	stopOnce     sync.Once
}

func (hub *roomHub) add(client *gatewayClient) {
	hub.mutex.Lock()
	hub.clients[client.id] = client
	hub.mutex.Unlock()
}

func (hub *roomHub) run() {
	defer hub.gateway.workers.Done()
	for {
		select {
		case <-hub.ctx.Done():
			return
		case event, open := <-hub.subscription.Messages():
			if !open {
				return
			}
			// This instance's own rooms already delivered it from memory.
			if event.SourceInstanceID == hub.gateway.coordinator.InstanceID() {
				continue
			}
			hub.broadcastEvent(event)
		case err, open := <-hub.subscription.Errors():
			if !open {
				return
			}
			if err != nil {
				hub.gateway.report(hub.roomID, err)
				hub.disconnectClients("room event subscription unavailable")
				return
			}
		}
	}
}

func (hub *roomHub) disconnectClients(reason string) {
	hub.mutex.RLock()
	defer hub.mutex.RUnlock()
	for _, client := range hub.clients {
		client.stop(fasthttpWebsocket.CloseTryAgainLater, reason)
	}
}

func (hub *roomHub) stop() {
	hub.stopOnce.Do(func() {
		hub.cancel()
		_ = hub.subscription.Close()
	})
}
