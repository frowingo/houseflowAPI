package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
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
type v2ClientState struct {
	epoch           int64
	sequence        int64
	sessionVersion  int64
	phase           houseRockets.Phase
	grant           *houseRockets.HouseRocketsControlGrantedModel
	frame           *gamePreviewFrame
	snapshots       chan []byte
	pending         []v2PendingMessage
	pendingSnapshot []byte
	controlSignal   chan struct{}
	needSnapshot    bool
	needBind        bool
	lastBind        time.Time
	steerLimit      connectionRateLimit
	pingLimit       connectionRateLimit
}

func newV2ClientState(epoch int64) *v2ClientState {
	return &v2ClientState{epoch: epoch, snapshots: make(chan []byte, 1), controlSignal: make(chan struct{}, 1), needSnapshot: true, needBind: true,
		steerLimit: connectionRateLimit{window: time.Second, maximum: houseRockets.MaximumSteerMessagesPerSecond}, pingLimit: connectionRateLimit{window: time.Second, maximum: 5}}
}

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
	welcome := NewV2ServerMessage(WelcomeMessageType, "", nil, time.Now(), houseRockets.HouseRocketsWelcomeModel{SessionID: client.roomID, GameKey: houseRockets.GameKey, ConnectionID: client.id, ProtocolVersion: 2, CourseVersion: houseRockets.CourseVersion, Settings: houseRockets.HouseRocketsRuntimeSettings()})
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
	for _, client := range clients {
		if client.v2 != nil {
			client.handleV2Event(event)
		} else {
			client.enqueue(serverMessageFromEvent(event))
		}
	}
}

func (client *gatewayClient) handleV2Event(event coordinationAbstract.MessageEnvelope) {
	sentAt := event.CreatedAt
	if sentAt.IsZero() {
		sentAt = time.Now()
	}
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
		var frame gamePreviewFrame
		if json.Unmarshal(event.Payload, &frame) != nil {
			return
		}
		client.enqueueGameFrame(frame, event.MessageID, event.ConnectionID != "", sentAt)
	case houseRockets.ResultMessageType:
		var result houseRockets.HouseRocketsResultModel
		if json.Unmarshal(event.Payload, &result) != nil || result.SessionID != client.roomID {
			return
		}
		client.enqueueV2(NewV2ServerMessage(event.Type, event.MessageID, nil, sentAt, result), "", 0)
	case houseRockets.ControlGrantedMessageType:
		if event.ConnectionID != client.id {
			return
		}
		var grant houseRockets.HouseRocketsControlGrantedModel
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

func (client *gatewayClient) enqueueGameFrame(frame gamePreviewFrame, messageID string, targeted bool, sentAt time.Time) {
	client.mutex.Lock()
	if frame.World.SessionID != client.roomID || frame.RuntimeEpoch < client.v2.epoch {
		client.mutex.Unlock()
		return
	}
	if frame.RuntimeEpoch == client.v2.epoch && frame.StateSequence <= client.v2.sequence {
		// A periodic frame may win the publisher select ahead of a targeted
		// resync response. Reply with the already newer authoritative frame;
		// never roll state back or silently lose the request correlation.
		if !targeted || client.v2.frame == nil {
			client.mutex.Unlock()
			return
		}
		frame = *client.v2.frame
	}
	if frame.RuntimeEpoch > client.v2.epoch {
		client.v2.grant = nil
		client.v2.sequence = 0
		client.v2.needBind = true
		client.v2.lastBind = time.Time{}
	}
	client.v2.epoch, client.v2.sequence, client.v2.phase = frame.RuntimeEpoch, frame.StateSequence, frame.Phase
	client.v2.frame = &frame
	model := houseRockets.SnapshotForConnection(frame.RuntimeFrame, client.userID, client.id, client.v2.grant)
	model.CountdownEndsAt = frame.CountdownEndsAt
	if !targeted {
		messageID = ""
	}
	payload, err := json.Marshal(NewV2ServerMessage(houseRockets.SnapshotMessageType, messageID, &frame.StateSequence, sentAt, model))
	if err == nil {
		if !client.active {
			client.v2.pendingSnapshot = payload
		} else if targeted || frame.Phase == houseRockets.PhaseEnded || frame.Phase == houseRockets.PhaseCancelled {
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
		decoded, err := DecodeV2ClientMessage(payload)
		if err != nil {
			client.reject(decoded.Envelope.MessageID, protocolErrorFromV2(err))
			if client.recordViolation() {
				return
			}
			continue
		}
		message := decoded.Envelope
		allowed := false
		switch message.Type {
		case houseRockets.SteerMessageType:
			allowed = client.v2.steerLimit.allow(time.Now())
		case PingMessageType:
			allowed = client.v2.pingLimit.allow(time.Now())
		default:
			allowed = client.rateLimit.allow(time.Now())
		}
		if !allowed {
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
			client.reject(message.MessageID, protocolErrorFromV2(err))
			if helpers.IsApplicationError(err, "house.error.user_not_member") {
				client.stop(websocket.ClosePolicyViolation, "house membership revoked")
				return
			}
		}
	}
}

func protocolErrorFromV2(err error) ProtocolError {
	code := ""
	switch {
	case errors.Is(err, houseRockets.ErrStaleInput), errors.Is(err, houseRockets.ErrControlRequired):
		code = houseRockets.StaleControlErrorCode
	case errors.Is(err, houseRockets.ErrPlayerEliminated):
		code = houseRockets.PlayerEliminatedErrorCode
	case errors.Is(err, houseRockets.ErrPlayerNotFound):
		code = houseRockets.ControlUnavailableErrorCode
	case errors.Is(err, houseRockets.ErrSimulationFinished), errors.Is(err, ErrRoomRuntimeNotStarted):
		code = houseRockets.NotRunningErrorCode
	case errors.Is(err, houseRockets.ErrInputRate):
		code = messageRateLimitErrorCode
	case errors.Is(err, houseRockets.ErrRuntimeBusy):
		return ProtocolError{Code: roomUnavailableErrorCode, Retryable: true}
	case errors.Is(err, houseRockets.ErrInvalidInput), errors.Is(err, houseRockets.ErrInputExpired):
		code = houseRockets.InvalidInputErrorCode
	}
	if code != "" {
		return ProtocolError{Code: code}
	}
	return protocolErrorFrom(err)
}

func (client *gatewayClient) handleV2Message(ctx context.Context, decoded DecodedV2ClientMessage) error {
	message := decoded.Envelope
	client.mutex.Lock()
	epoch := client.v2.epoch
	grant := client.v2.grant
	phase := client.v2.phase
	controlsOwnPlayer := false
	if client.v2.frame != nil && grant != nil && phase == houseRockets.PhasePlaying {
		for index, player := range client.v2.frame.World.Players {
			if player.PlayerID == client.userID && player.IsAlive && index < len(client.v2.frame.Controls) && client.v2.frame.Controls[index].ConnectionID == client.id {
				controlsOwnPlayer = true
			}
		}
	}
	client.mutex.Unlock()
	switch message.Type {
	case houseRockets.SteerMessageType:
		if grant == nil {
			return houseRockets.ErrControlRequired
		}
		return client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.userID, client.id, houseRockets.RuntimeInput{Kind: houseRockets.InputSteer, RuntimeEpoch: epoch, ControlGeneration: decoded.Steer.ControlGeneration, InputSequence: decoded.Steer.InputSequence, Heading: decoded.Steer.Heading}, message.MessageID)
	case PingMessageType:
		client.enqueueV2(NewV2ServerMessage(PongMessageType, message.MessageID, nil, time.Now(), dtos.RealtimePongModel{PingID: decoded.Ping.PingID, ServerTime: time.Now().UTC()}), "", 0)
		if controlsOwnPlayer {
			return client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.userID, client.id, houseRockets.RuntimeInput{Kind: houseRockets.InputHeartbeat, RuntimeEpoch: epoch, ControlGeneration: grant.ControlGeneration}, message.MessageID)
		}
		if phase == houseRockets.PhasePlaying || phase == houseRockets.PhaseFinalizing || phase == houseRockets.PhaseEnded || phase == houseRockets.PhaseCancelled {
			return nil
		}
		return client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.userID, client.id, houseRockets.RuntimeInput{Kind: lobbyHeartbeatInput, RuntimeEpoch: epoch}, message.MessageID)
	case houseRockets.ResyncMessageType:
		snapshot, err := client.verifyV2Access(ctx)
		if err != nil {
			return err
		}
		client.enqueueV2(NewV2ServerMessage(GameSessionSnapshotEventType, message.MessageID, &snapshot.Version, time.Now(), dtos.GameSessionToResponseModel(snapshot)), "", 0)
		client.mutex.Lock()
		client.v2.needBind = true
		client.v2.lastBind = time.Time{}
		client.mutex.Unlock()
		return client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.userID, client.id, houseRockets.RuntimeInput{Kind: houseRockets.InputSnapshot, RuntimeEpoch: epoch}, message.MessageID)
	default:
		if _, err := client.verifyV2Access(ctx); err != nil {
			return err
		}
		if message.Type == gameCommands.SetPlayerReadyCommandType {
			if err := client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.userID, client.id, houseRockets.RuntimeInput{Kind: lobbyHeartbeatInput, RuntimeEpoch: epoch}); err != nil {
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
			ctx, cancel := context.WithTimeout(client.ctx, client.gateway.options.WriteTimeout)
			err := client.gateway.coordinator.TouchPresence(ctx, presence, client.gateway.options.PresenceTTL)
			cancel()
			if err != nil {
				client.stop(websocket.CloseTryAgainLater, "presence unavailable")
				return
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
						client.v2.phase = houseRockets.PhaseRecovering
						client.v2.needSnapshot, client.v2.needBind = true, true
						client.v2.lastBind = time.Time{}
					}
					client.mutex.Unlock()
				}
			}
			cancel()
			client.signalControl()
		case <-client.v2.controlSignal:
			client.mutex.Lock()
			epoch := client.v2.epoch
			needSnapshot := client.v2.needSnapshot
			client.v2.needSnapshot = false
			needBind := client.v2.needBind && client.v2.phase == houseRockets.PhasePlaying && time.Since(client.v2.lastBind) >= time.Second
			if needBind {
				alive := false
				if client.v2.frame != nil {
					for _, player := range client.v2.frame.World.Players {
						if player.PlayerID == client.userID && player.IsAlive {
							alive = true
						}
					}
				}
				needBind = alive
			}
			if needBind {
				client.v2.lastBind = time.Now()
			}
			client.mutex.Unlock()
			if !needSnapshot && !needBind {
				continue
			}
			ctx, cancel := context.WithTimeout(client.ctx, client.gateway.options.CommandTimeout)
			_, err := client.verifyV2Access(ctx)
			if err == nil && needSnapshot {
				err = client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.userID, client.id, houseRockets.RuntimeInput{Kind: houseRockets.InputSnapshot, RuntimeEpoch: epoch})
			}
			if err == nil && needBind {
				err = client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.userID, client.id, houseRockets.RuntimeInput{Kind: houseRockets.InputBind, RuntimeEpoch: epoch})
			}
			cancel()
			if err != nil && !errors.Is(err, ErrRoomRuntimeNotStarted) {
				client.reject("", protocolErrorFromV2(err))
			}
		}
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
	_ = client.gateway.manager.DispatchGameplay(ctx, client.roomID, client.userID, client.id, houseRockets.RuntimeInput{Kind: houseRockets.InputDisconnect, RuntimeEpoch: epoch, ControlGeneration: grant.ControlGeneration})
}
