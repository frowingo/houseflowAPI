package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
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
}

type Gateway struct {
	coordinator coordinationAbstract.Coordinator
	manager     *RoomManager
	sender      cqrs.Sender
	options     GatewayOptions

	mutex   sync.Mutex
	hubs    map[string]*roomHub
	clients map[string]*gatewayClient
	started bool
	closed  bool
	ctx     context.Context
	cancel  context.CancelFunc
	errors  chan RuntimeError
	workers sync.WaitGroup
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
	if options.PingInterval >= options.IdleTimeout {
		return nil, errors.New("ping interval must be shorter than idle timeout")
	}
	if options.PresenceRefresh >= options.PresenceTTL {
		return nil, errors.New("presence refresh interval must be shorter than presence TTL")
	}
	return &Gateway{
		coordinator: coordinator,
		manager:     manager,
		sender:      sender,
		options:     options,
		hubs:        make(map[string]*roomHub),
		clients:     make(map[string]*gatewayClient),
		errors:      make(chan RuntimeError, 32),
	}, nil
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
		roomID := strings.TrimSpace(c.Params("sessionId"))
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
		if _, err := cqrs.Send[gameDomain.SessionSnapshot](requestCtx, gateway.sender, gameQueries.GetGameSessionQuery{
			SessionID: roomID,
			UserID:    userID,
		}); err != nil {
			return helpers.RespondLocalizedError(c, gateway.options.Localizer, err)
		}
		if _, err := gateway.manager.EnsureRoom(requestCtx, roomID); err != nil {
			if errors.Is(err, ErrTerminalRoom) {
				err = helpers.NewConflictError("game.error.invalid_state")
			} else {
				err = helpers.NewUnavailableError(roomUnavailableErrorCode, err)
			}
			return helpers.RespondLocalizedError(c, gateway.options.Localizer, err)
		}
		return c.Next()
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
	roomID := socket.Params("sessionId")
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
	if !connection.activate(snapshotMessage(snapshot)) {
		connection.stop(fasthttpWebsocket.ClosePolicyViolation, "outbound queue unavailable")
		return
	}

	if !gateway.startWorker() {
		return
	}
	writerStarted = true
	go connection.writeLoop(presence)
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
	pending    []ServerMessage
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
	return &gatewayClient{
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
}

func (client *gatewayClient) activate(initial ServerMessage) bool {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	messages := make([]ServerMessage, 0, len(client.pending)+1)
	messages = append(messages, initial)
	for _, pending := range client.pending {
		if pending.Type == GameSessionSnapshotEventType && pending.Sequence <= initial.Sequence {
			continue
		}
		messages = append(messages, pending)
	}
	if len(messages) > cap(client.outbound) {
		return false
	}
	for _, message := range messages {
		payload, err := json.Marshal(message)
		if err != nil {
			return false
		}
		client.outbound <- payload
	}
	client.pending = nil
	client.active = true
	return true
}

func (client *gatewayClient) enqueue(message ServerMessage) bool {
	client.mutex.Lock()
	if !client.active {
		if len(client.pending) >= cap(client.outbound)-1 {
			client.mutex.Unlock()
			client.stop(fasthttpWebsocket.ClosePolicyViolation, "slow consumer")
			return false
		}
		client.pending = append(client.pending, message)
		client.mutex.Unlock()
		return true
	}
	payload, err := json.Marshal(message)
	if err != nil {
		client.mutex.Unlock()
		return false
	}
	select {
	case client.outbound <- payload:
		client.mutex.Unlock()
		return true
	default:
		client.mutex.Unlock()
		client.stop(fasthttpWebsocket.ClosePolicyViolation, "slow consumer")
		return false
	}
}

func (client *gatewayClient) readLoop() {
	client.socket.SetReadLimit(client.gateway.options.MaximumMessageBytes)
	_ = client.socket.SetReadDeadline(time.Now().Add(client.gateway.options.IdleTimeout))
	client.socket.SetPongHandler(func(string) error {
		return client.socket.SetReadDeadline(time.Now().Add(client.gateway.options.IdleTimeout))
	})
	for {
		messageType, payload, err := client.socket.ReadMessage()
		if err != nil {
			return
		}
		_ = client.socket.SetReadDeadline(time.Now().Add(client.gateway.options.IdleTimeout))
		if messageType != fasthttpWebsocket.TextMessage {
			client.reject("", ProtocolError{Code: defaultInvalidCommandErrorCode})
			if client.recordViolation() {
				return
			}
			continue
		}
		if !client.rateLimit.allow(time.Now()) {
			client.reject("", ProtocolError{Code: messageRateLimitErrorCode, Retryable: true})
			if client.recordViolation() {
				return
			}
			continue
		}
		message, err := decodeClientMessage(payload)
		if err != nil {
			client.reject(message.MessageID, protocolErrorFrom(err))
			if client.recordViolation() {
				return
			}
			continue
		}
		client.violations = 0
		if !client.enqueue(newServerMessage(CommandAcceptedMessageType, message.MessageID)) {
			return
		}
		commandCtx, cancel := context.WithTimeout(client.ctx, client.gateway.options.CommandTimeout)
		err = client.gateway.manager.Dispatch(commandCtx, coordinationAbstract.MessageEnvelope{
			MessageID:    message.MessageID,
			RoomID:       client.roomID,
			Type:         message.Type,
			ActorID:      client.userID,
			ConnectionID: client.id,
			Payload:      message.Payload,
		})
		cancel()
		if err != nil {
			client.reject(message.MessageID, ProtocolError{Code: roomUnavailableErrorCode, Retryable: true})
		}
	}
}

func (client *gatewayClient) recordViolation() bool {
	client.violations++
	if client.violations < 3 {
		return false
	}
	client.stop(fasthttpWebsocket.ClosePolicyViolation, "too many protocol violations")
	return true
}

func (client *gatewayClient) writeLoop(presence coordinationAbstract.Presence) {
	defer client.gateway.workers.Done()
	defer close(client.writerDone)
	pingTicker := time.NewTicker(client.gateway.options.PingInterval)
	presenceTicker := time.NewTicker(client.gateway.options.PresenceRefresh)
	defer pingTicker.Stop()
	defer presenceTicker.Stop()
	for {
		select {
		case <-client.ctx.Done():
			client.stop(fasthttpWebsocket.CloseGoingAway, "realtime gateway stopping")
			return
		case payload := <-client.outbound:
			_ = client.socket.SetWriteDeadline(time.Now().Add(client.gateway.options.WriteTimeout))
			if err := client.socket.WriteMessage(fasthttpWebsocket.TextMessage, payload); err != nil {
				client.stop(fasthttpWebsocket.CloseAbnormalClosure, "write failed")
				return
			}
		case <-pingTicker.C:
			deadline := time.Now().Add(client.gateway.options.WriteTimeout)
			if err := client.socket.WriteControl(fasthttpWebsocket.PingMessage, nil, deadline); err != nil {
				client.stop(fasthttpWebsocket.CloseAbnormalClosure, "ping failed")
				return
			}
		case <-presenceTicker.C:
			presenceCtx, cancel := context.WithTimeout(client.ctx, client.gateway.options.WriteTimeout)
			err := client.gateway.coordinator.TouchPresence(
				presenceCtx,
				presence,
				client.gateway.options.PresenceTTL,
			)
			cancel()
			if err != nil {
				client.gateway.report(client.roomID, err)
				client.stop(fasthttpWebsocket.CloseTryAgainLater, "presence unavailable")
				return
			}
		}
	}
}

func (client *gatewayClient) reject(messageID string, protocolError ProtocolError) {
	message := newServerMessage(CommandRejectedMessageType, messageID)
	message.Error = &protocolError
	client.enqueue(message)
}

func (client *gatewayClient) stop(code int, reason string) {
	client.stopOnce.Do(func() {
		client.cancel()
		deadline := time.Now().Add(client.gateway.options.WriteTimeout)
		_ = client.socket.WriteControl(
			fasthttpWebsocket.CloseMessage,
			fasthttpWebsocket.FormatCloseMessage(code, reason),
			deadline,
		)
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
			hub.broadcast(serverMessageFromEvent(event), event.ConnectionID)
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

func (hub *roomHub) broadcast(message ServerMessage, targetConnectionID string) {
	hub.mutex.RLock()
	defer hub.mutex.RUnlock()
	if targetConnectionID != "" {
		if client := hub.clients[targetConnectionID]; client != nil {
			client.enqueue(message)
		}
		return
	}
	for _, client := range hub.clients {
		client.enqueue(message)
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

func snapshotMessage(snapshot gameDomain.SessionSnapshot) ServerMessage {
	payload, _ := json.Marshal(snapshot)
	message := newServerMessage(GameSessionSnapshotEventType, "")
	message.Sequence = snapshot.Version
	message.Payload = payload
	return message
}

func serverMessageFromEvent(event coordinationAbstract.MessageEnvelope) ServerMessage {
	message := ServerMessage{
		ProtocolVersion: ProtocolVersion,
		MessageID:       event.MessageID,
		Type:            event.Type,
		Sequence:        event.Sequence,
		SentAt:          event.CreatedAt,
	}
	if message.SentAt.IsZero() {
		message.SentAt = time.Now().UTC()
	}
	if event.Type == CommandRejectedMessageType {
		var protocolError ProtocolError
		if err := json.Unmarshal(event.Payload, &protocolError); err != nil {
			protocolError = ProtocolError{Code: defaultInternalCommandErrorCode, Retryable: true}
		}
		message.Error = &protocolError
	} else {
		message.Payload = event.Payload
	}
	return message
}
