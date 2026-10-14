package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	websocket "github.com/fasthttp/websocket"
)

type v2PendingMessage struct {
	payload  []byte
	kind     string
	sequence int64
}

// tickFrame is a published runtime frame with its header decoded once per hub.
type tickFrame struct {
	header  module.FrameHeader
	payload json.RawMessage
}

// v2ClientState is one connection's view of its room's runtime: the newest
// frame and epoch it has seen, its control grant and the work it still owes
// the runtime (a snapshot request or a control bind).
type v2ClientState struct {
	game            module.TickModule
	epoch           int64
	sequence        int64
	sessionVersion  int64
	phase           module.Phase
	grant           *module.ControlGrant
	frame           *tickFrame
	snapshots       chan []byte
	pending         []v2PendingMessage
	pendingSnapshot []byte
	controlSignal   chan struct{}
	needSnapshot    bool
	needBind        bool
	lastBind        time.Time
	intentLimit     connectionRateLimit
	pingLimit       connectionRateLimit
}

func newV2ClientState(game module.TickModule, epoch int64) *v2ClientState {
	return &v2ClientState{game: game, epoch: epoch, snapshots: make(chan []byte, 1), controlSignal: make(chan struct{}, 1), needSnapshot: true, needBind: true,
		intentLimit: connectionRateLimit{window: time.Second, maximum: game.Timing().MaximumIntentRate}, pingLimit: connectionRateLimit{window: time.Second, maximum: 5}}
}

func (state *v2ClientState) gameKey() string { return state.game.Definition().GameKey }

func (gateway *Gateway) requireActiveMember(ctx context.Context, houseID, userID string) error {
	participants, err := gateway.manager.options.ParticipantDirectory.ReadHouseParticipants(ctx, houseID)
	if err != nil {
		return helpers.NewUnavailableError(roomUnavailableErrorCode, err)
	}
	for _, participant := range participants {
		if participant.PlayerID == userID {
			return nil
		}
	}
	return helpers.NewForbiddenError("house.error.user_not_member")
}

func (client *gatewayClient) verifyV2Access(ctx context.Context) (gameDomain.SessionSnapshot, error) {
	snapshot, err := cqrs.Send[gameDomain.SessionSnapshot](ctx, client.gateway.sender, gameQueries.GetGameSessionQuery{SessionID: client.roomID, UserID: client.userID})
	if err != nil {
		return snapshot, err
	}
	return snapshot, client.gateway.requireActiveMember(ctx, snapshot.HouseID, client.userID)
}

func (client *gatewayClient) enqueueV2(message any, kind string, sequence int64) bool {
	payload, err := json.Marshal(message)
	if err != nil {
		return false
	}
	client.mutex.Lock()
	if !client.active {
		if len(client.v2.pending) >= cap(client.outbound)-2 {
			client.mutex.Unlock()
			client.stop(websocket.ClosePolicyViolation, "slow consumer")
			return false
		}
		client.v2.pending = append(client.v2.pending, v2PendingMessage{payload, kind, sequence})
		client.mutex.Unlock()
		return true
	}
	if kind == GameSessionSnapshotEventType && sequence <= client.v2.sessionVersion {
		client.mutex.Unlock()
		return true
	}
	if kind == GameSessionSnapshotEventType {
		client.v2.sessionVersion = sequence
	}
	select {
	case client.outbound <- payload:
		client.mutex.Unlock()
		return true
	default:
		client.mutex.Unlock()
		client.stop(websocket.ClosePolicyViolation, "slow consumer")
		return false
	}
}

func (client *gatewayClient) activateV2(snapshot gameDomain.SessionSnapshot) bool {
	welcome := NewV2ServerMessage(WelcomeMessageType, "", nil, time.Now(), client.v2.game.Welcome(client.roomID, client.id))
	initial := NewV2ServerMessage(GameSessionSnapshotEventType, "", &snapshot.Version, time.Now(), dtos.GameSessionToResponseModel(snapshot))
	welcomeBytes, _ := json.Marshal(welcome)
	initialBytes, _ := json.Marshal(initial)
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if cap(client.outbound) < 2 {
		return false
	}
	client.outbound <- welcomeBytes
	client.outbound <- initialBytes
	client.v2.sessionVersion = snapshot.Version
	for _, pending := range client.v2.pending {
		if pending.kind == GameSessionSnapshotEventType && pending.sequence <= client.v2.sessionVersion {
			continue
		}
		if pending.kind == GameSessionSnapshotEventType {
			client.v2.sessionVersion = pending.sequence
		}
		select {
		case client.outbound <- pending.payload:
		default:
			return false
		}
	}
	client.v2.pending = nil
	client.active = true
	if client.v2.pendingSnapshot != nil {
		client.v2.snapshots <- client.v2.pendingSnapshot
		client.v2.pendingSnapshot = nil
	}
	client.signalControl()
	return true
}

func (client *gatewayClient) signalControl() {
	select {
	case client.v2.controlSignal <- struct{}{}:
	default:
	}
}

func (hub *roomHub) broadcastEvent(event coordinationAbstract.MessageEnvelope) {
	hub.mutex.RLock()
	clients := make([]*gatewayClient, 0, len(hub.clients))
	for _, client := range hub.clients {
		if event.ConnectionID == "" || event.ConnectionID == client.id {
			clients = append(clients, client)
		}
	}
	hub.mutex.RUnlock()
	var frame *tickFrame
	if event.Type == GameRuntimeSnapshotEventType {
		frame = &tickFrame{payload: event.Payload}
		if json.Unmarshal(event.Payload, &frame.header) != nil {
			return
		}
	}
	for _, client := range clients {
		client.handleV2Event(event, frame)
	}
}

func (client *gatewayClient) handleV2Event(event coordinationAbstract.MessageEnvelope, frame *tickFrame) {
	sentAt := event.CreatedAt
	if sentAt.IsZero() {
		sentAt = time.Now()
	}
	gameKey := client.v2.gameKey()
	switch event.Type {
	case GameRoomAccessEventType:
		var members []string
		if json.Unmarshal(event.Payload, &members) != nil {
			client.stop(websocket.CloseTryAgainLater, "invalid access update")
			return
		}
		for _, member := range members {
			if member == client.userID {
				return
			}
		}
		client.stop(websocket.ClosePolicyViolation, "house membership revoked")
	case GameSessionSnapshotEventType:
		var snapshot gameDomain.SessionSnapshot
		if json.Unmarshal(event.Payload, &snapshot) != nil {
			return
		}
		client.enqueueV2(NewV2ServerMessage(event.Type, event.MessageID, &snapshot.Version, sentAt, dtos.GameSessionToResponseModel(snapshot)), event.Type, snapshot.Version)
		client.signalControl()
	case GameRuntimeSnapshotEventType:
		if frame != nil {
			client.enqueueGameFrame(*frame, event.MessageID, event.ConnectionID != "", sentAt)
		}
	case module.ResultMessageType(gameKey):
		var result struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(event.Payload, &result) != nil || result.SessionID != client.roomID {
			return
		}
		client.enqueueV2(NewV2ServerMessage(event.Type, event.MessageID, nil, sentAt, json.RawMessage(event.Payload)), "", 0)
	case module.ControlGrantedMessageType(gameKey):
		if event.ConnectionID != client.id {
			return
		}
		var grant module.ControlGrant
		if json.Unmarshal(event.Payload, &grant) != nil || grant.PlayerID != client.userID || grant.SessionID != client.roomID {
			return
		}
		client.mutex.Lock()
		if grant.RuntimeEpoch < client.v2.epoch {
			client.mutex.Unlock()
			return
		}
		if grant.RuntimeEpoch > client.v2.epoch {
			client.v2.sequence = 0
			client.v2.frame = nil
			client.v2.needSnapshot = true
		}
		client.v2.epoch = grant.RuntimeEpoch
		client.v2.grant = &grant
		client.v2.needBind = false
		client.mutex.Unlock()
		client.enqueueV2(NewV2ServerMessage(event.Type, event.MessageID, nil, sentAt, grant), "", 0)
	case CommandRejectedMessageType:
		var protocolError ProtocolError
		if json.Unmarshal(event.Payload, &protocolError) != nil {
			protocolError = ProtocolError{Code: defaultInternalCommandErrorCode, Retryable: true}
		}
		client.reject(event.MessageID, protocolError)
	}
}

func (client *gatewayClient) enqueueGameFrame(frame tickFrame, messageID string, targeted bool, sentAt time.Time) {
	client.mutex.Lock()
	if frame.header.World.SessionID != client.roomID || frame.header.RuntimeEpoch < client.v2.epoch {
		client.mutex.Unlock()
		return
	}
	if frame.header.RuntimeEpoch == client.v2.epoch && frame.header.StateSequence <= client.v2.sequence {
		// A periodic frame may win the publisher select ahead of a targeted
		// resync response. Reply with the already newer authoritative frame;
		// never roll state back or silently lose the request correlation.
		if !targeted || client.v2.frame == nil {
			client.mutex.Unlock()
			return
		}
		frame = *client.v2.frame
	}
	if frame.header.RuntimeEpoch > client.v2.epoch {
		client.v2.grant = nil
		client.v2.sequence = 0
		client.v2.needBind = true
		client.v2.lastBind = time.Time{}
	}
	client.v2.epoch, client.v2.sequence, client.v2.phase = frame.header.RuntimeEpoch, frame.header.StateSequence, frame.header.Phase
	client.v2.frame = &frame
	if !targeted {
		messageID = ""
	}
	payload, err := client.projectFrame(frame, messageID, sentAt)
	if err == nil {
		if !client.active {
			client.v2.pendingSnapshot = payload
		} else if targeted || frame.header.Phase == module.PhaseEnded || frame.header.Phase == module.PhaseCancelled {
			// Terminal snapshots are control messages, not disposable motion
			// samples. Keep them ordered before the committed result event.
			select {
			case <-client.v2.snapshots:
			default:
			}
			select {
			case client.outbound <- payload:
			default:
				client.mutex.Unlock()
				client.stop(websocket.ClosePolicyViolation, "slow consumer")
				return
			}
		} else {
			select {
			case <-client.v2.snapshots:
				client.gateway.options.Metrics.framesCoalesced.Add(1)
			default:
			}
			client.v2.snapshots <- payload
		}
	}
	client.mutex.Unlock()
	client.signalControl()
}

// projectFrame renders the frame as this connection's public snapshot; only
// its own control generation is ever included. Called with client.mutex held.
func (client *gatewayClient) projectFrame(frame tickFrame, messageID string, sentAt time.Time) ([]byte, error) {
	model, err := client.v2.game.ProjectFrame(frame.payload, module.Viewer{PlayerID: client.userID, ConnectionID: client.id, Grant: client.v2.grant})
	if err != nil {
		return nil, err
	}
	return json.Marshal(NewV2ServerMessage(module.SnapshotMessageType(client.v2.gameKey()), messageID, &frame.header.StateSequence, sentAt, model))
}

func (client *gatewayClient) readV2Loop() {
	for {
		if client.ctx.Err() != nil {
			return
		}
		kind, payload, err := client.socket.ReadMessage()
		if err != nil {
			return
		}
		client.gateway.options.Metrics.inputBytes.Add(uint64(len(payload)))
		_ = client.socket.SetReadDeadline(time.Now().Add(client.gateway.options.IdleTimeout))
		if kind != websocket.TextMessage {
			client.reject("", ProtocolError{Code: defaultInvalidCommandErrorCode})
			if client.recordViolation() {
				return
			}
			continue
		}
		decoded, err := DecodeV2ClientMessage(payload, client.v2.game)
		if err != nil {
			client.reject(decoded.Envelope.MessageID, tickProtocolError(client.v2.gameKey(), err))
			if client.recordViolation() {
				return
			}
			continue
		}
		message := decoded.Envelope
		if !client.allow(decoded) {
			client.reject(message.MessageID, ProtocolError{Code: messageRateLimitErrorCode, Retryable: true})
			if client.recordViolation() {
				return
			}
			continue
		}
		client.violations = 0
		ctx, cancel := context.WithTimeout(client.ctx, client.gateway.options.CommandTimeout)
		err = client.handleV2Message(ctx, decoded)
		cancel()
		if err != nil {
			client.reject(message.MessageID, tickProtocolError(client.v2.gameKey(), err))
			if helpers.IsApplicationError(err, "house.error.user_not_member") {
				client.stop(websocket.ClosePolicyViolation, "house membership revoked")
				return
			}
		}
	}
}

// allow applies the gameplay, clock and command rate limits separately, so
// steering cannot starve lobby commands and vice versa.
func (client *gatewayClient) allow(decoded DecodedV2ClientMessage) bool {
	now := time.Now()
	switch {
	case decoded.Intent != nil:
		return client.v2.intentLimit.allow(now)
	case decoded.Envelope.Type == PingMessageType:
		return client.v2.pingLimit.allow(now)
	default:
		return client.rateLimit.allow(now)
	}
}

// controlInput is the authenticated identity of this connection for one input.
func (client *gatewayClient) controlInput(kind, messageID string, epoch int64) module.ControlInput {
	return module.ControlInput{MessageID: messageID, Kind: kind, PlayerID: client.userID, ConnectionID: client.id, RuntimeEpoch: epoch}
}

func (client *gatewayClient) handleV2Message(ctx context.Context, decoded DecodedV2ClientMessage) error {
	message := decoded.Envelope
	client.mutex.Lock()
	epoch := client.v2.epoch
	grant := client.v2.grant
	phase := client.v2.phase
	controlsOwnPlayer := client.v2.frame != nil && grant != nil && client.v2.frame.header.ControlledBy(client.userID, client.id)
	client.mutex.Unlock()
	switch {
	case decoded.Intent != nil:
		if grant == nil {
			return module.ErrControlRequired
		}
		return client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.controlInput("", message.MessageID, epoch), decoded.Intent)
	case message.Type == PingMessageType:
		client.enqueueV2(NewV2ServerMessage(PongMessageType, message.MessageID, nil, time.Now(), dtos.RealtimePongModel{PingID: decoded.Ping.PingID, ServerTime: time.Now().UTC()}), "", 0)
		if controlsOwnPlayer {
			control := client.controlInput(module.InputHeartbeat, message.MessageID, epoch)
			control.ControlGeneration = grant.ControlGeneration
			return client.gateway.manager.DispatchGameplay(ctx, client.roomID, control, nil)
		}
		if phase == module.PhasePlaying || phase == module.PhaseFinalizing || phase == module.PhaseEnded || phase == module.PhaseCancelled {
			return nil
		}
		return client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.controlInput(lobbyHeartbeatInput, message.MessageID, epoch), nil)
	case message.Type == module.ResyncMessageType(client.v2.gameKey()):
		snapshot, err := client.verifyV2Access(ctx)
		if err != nil {
			return err
		}
		client.enqueueV2(NewV2ServerMessage(GameSessionSnapshotEventType, message.MessageID, &snapshot.Version, time.Now(), dtos.GameSessionToResponseModel(snapshot)), "", 0)
		client.mutex.Lock()
		client.v2.needBind = true
		client.v2.lastBind = time.Time{}
		client.mutex.Unlock()
		return client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.controlInput(module.InputSnapshot, message.MessageID, epoch), nil)
	default:
		if _, err := client.verifyV2Access(ctx); err != nil {
			return err
		}
		if message.Type == gameCommands.SetPlayerReadyCommandType {
			if err := client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.controlInput(lobbyHeartbeatInput, "", epoch), nil); err != nil {
				return err
			}
		}
		if err := client.gateway.manager.Dispatch(ctx, coordinationAbstract.MessageEnvelope{MessageID: message.MessageID, RoomID: client.roomID, ActorID: client.userID, ConnectionID: client.id, Type: message.Type, Payload: message.Payload}); err != nil {
			return err
		}
		client.enqueueV2(NewV2ServerMessage(CommandAcceptedMessageType, message.MessageID, nil, time.Now(), struct{}{}), "", 0)
		return nil
	}
}

func (client *gatewayClient) controlLoop(presence coordinationAbstract.Presence) {
	defer client.gateway.workers.Done()
	ticker := time.NewTicker(client.gateway.options.PresenceRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-client.ctx.Done():
			return
		case <-ticker.C:
			if !client.refreshPresence(presence) {
				return
			}
		case <-client.v2.controlSignal:
			client.requestOwedControl()
		}
	}
}

// refreshPresence keeps this socket visible and re-elects an owner for a room
// whose owner disappeared; a newer epoch requires a fresh snapshot and bind.
func (client *gatewayClient) refreshPresence(presence coordinationAbstract.Presence) bool {
	ctx, cancel := context.WithTimeout(client.ctx, client.gateway.options.WriteTimeout)
	err := client.gateway.coordinator.TouchPresence(ctx, presence, client.gateway.options.PresenceTTL)
	cancel()
	if err != nil {
		client.stop(websocket.CloseTryAgainLater, "presence unavailable")
		return false
	}
	ctx, cancel = context.WithTimeout(client.ctx, client.gateway.options.CommandTimeout)
	_, exists, ownerErr := client.gateway.coordinator.CurrentRoomOwner(ctx, client.roomID)
	if ownerErr == nil && !exists {
		ownership, err := client.gateway.manager.EnsureRoom(ctx, client.roomID)
		if err == nil {
			client.mutex.Lock()
			if ownership.RuntimeEpoch > client.v2.epoch {
				client.v2.epoch, client.v2.sequence = ownership.RuntimeEpoch, 0
				client.v2.grant, client.v2.frame = nil, nil
				client.v2.phase = module.PhaseRecovering
				client.v2.needSnapshot, client.v2.needBind = true, true
				client.v2.lastBind = time.Time{}
			}
			client.mutex.Unlock()
		}
	}
	cancel()
	client.signalControl()
	return true
}

// requestOwedControl asks the runtime for a snapshot and, while this player is
// alive in play, rebinds its controls at most once per second.
func (client *gatewayClient) requestOwedControl() {
	client.mutex.Lock()
	epoch := client.v2.epoch
	needSnapshot := client.v2.needSnapshot
	client.v2.needSnapshot = false
	needBind := client.v2.needBind && client.v2.phase == module.PhasePlaying && time.Since(client.v2.lastBind) >= time.Second &&
		client.v2.frame != nil && client.v2.frame.header.Alive(client.userID)
	if needBind {
		client.v2.lastBind = time.Now()
	}
	client.mutex.Unlock()
	if !needSnapshot && !needBind {
		return
	}
	ctx, cancel := context.WithTimeout(client.ctx, client.gateway.options.CommandTimeout)
	defer cancel()
	_, err := client.verifyV2Access(ctx)
	if err == nil && needSnapshot {
		err = client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.controlInput(module.InputSnapshot, "", epoch), nil)
	}
	if err == nil && needBind {
		err = client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.controlInput(module.InputBind, "", epoch), nil)
	}
	if err != nil && !errors.Is(err, ErrRoomRuntimeNotStarted) {
		client.reject("", tickProtocolError(client.v2.gameKey(), err))
	}
}

func (client *gatewayClient) disconnectController() {
	client.mutex.Lock()
	grant := client.v2.grant
	epoch := client.v2.epoch
	client.mutex.Unlock()
	if grant == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectionCleanupTimeout)
	defer cancel()
	control := client.controlInput(module.InputDisconnect, "", epoch)
	control.ControlGeneration = grant.ControlGeneration
	_ = client.gateway.manager.DispatchGameplay(ctx, client.roomID, control, nil)
}
