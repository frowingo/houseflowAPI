package realtime

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
)

const (
	V2ProtocolVersion  = 2
	WelcomeMessageType = "realtime.welcome"
	PingMessageType    = "realtime.ping"
	PongMessageType    = "realtime.pong"
)

type DecodedV2ClientMessage struct {
	Envelope ClientMessage
	Ready    *dtos.RealtimeReadyModel
	Ping     *dtos.RealtimePingModel
	// Intent is a tick game's gameplay message, validated by its module.
	Intent module.Intent
	// Move is a turn game's canonical move payload, validated by its module.
	Move json.RawMessage
}

type V2ServerMessage[T any] struct {
	ProtocolVersion int       `json:"protocolVersion"`
	MessageID       string    `json:"messageId,omitempty"`
	Type            string    `json:"type"`
	Sequence        *int64    `json:"sequence,omitempty"`
	SentAt          time.Time `json:"sentAt"`
	Payload         T         `json:"payload"`
}

type V2ErrorMessage struct {
	ProtocolVersion int             `json:"protocolVersion"`
	MessageID       string          `json:"messageId,omitempty"`
	Type            string          `json:"type"`
	SentAt          time.Time       `json:"sentAt"`
	Error           V2ProtocolError `json:"error"`
}

type V2ProtocolError struct {
	Code      string   `json:"code"`
	Args      []string `json:"args"`
	Retryable bool     `json:"retryable"`
}

func NewV2ErrorMessage(messageID string, sentAt time.Time, protocolError V2ProtocolError) V2ErrorMessage {
	if protocolError.Args == nil {
		protocolError.Args = []string{}
	}
	return V2ErrorMessage{
		ProtocolVersion: V2ProtocolVersion,
		MessageID:       messageID,
		Type:            CommandRejectedMessageType,
		SentAt:          sentAt.UTC(),
		Error:           protocolError,
	}
}

func V2ProtocolErrorFrom(gameKey string, err error) V2ProtocolError {
	result := tickProtocolError(gameKey, err)
	if result.Args == nil {
		result.Args = []string{}
	}
	return V2ProtocolError{Code: result.Code, Args: result.Args, Retryable: result.Retryable}
}

func NewV2ServerMessage[T any](messageType string, messageID string, sequence *int64, sentAt time.Time, payload T) V2ServerMessage[T] {
	return V2ServerMessage[T]{
		ProtocolVersion: V2ProtocolVersion,
		MessageID:       messageID,
		Type:            messageType,
		Sequence:        sequence,
		SentAt:          sentAt.UTC(),
		Payload:         payload,
	}
}

// DecodeV2ClientMessage strictly decodes one client envelope. Lobby commands
// and realtime controls are shared; game must decode its own gameplay intents.
func DecodeV2ClientMessage(data []byte, game module.Game) (DecodedV2ClientMessage, error) {
	var decoded DecodedV2ClientMessage
	if len(data) > defaultMaximumMessageBytes {
		return decoded, ErrInvalidClientMessage
	}
	if err := helpers.DecodeStrictObject(data, &decoded.Envelope, []string{"payload"}, "protocolVersion", "messageId", "type"); err != nil {
		return decoded, errors.Join(ErrInvalidClientMessage, err)
	}
	message := &decoded.Envelope
	if message.ProtocolVersion != V2ProtocolVersion {
		return decoded, ErrUnsupportedProtocol
	}
	if message.MessageID == "" || strings.TrimSpace(message.MessageID) != message.MessageID ||
		len(message.MessageID) > maximumMessageIDLength {
		return decoded, ErrInvalidClientMessage
	}
	var err error
	switch {
	case message.Type == gameCommands.JoinGameSessionCommandType, message.Type == gameCommands.LeaveGameSessionCommandType,
		message.Type == gameCommands.CancelGameSessionCommandType, game != nil && message.Type == module.ResyncMessageType(game.Definition().GameKey):
		if len(message.Payload) > 0 {
			var empty struct{}
			err = helpers.DecodeStrictObject(message.Payload, &empty, nil)
		}
	case message.Type == gameCommands.SetPlayerReadyCommandType:
		decoded.Ready = new(dtos.RealtimeReadyModel)
		err = helpers.DecodeStrictObject(message.Payload, decoded.Ready, nil, "ready")
	case message.Type == PingMessageType:
		decoded.Ping = new(dtos.RealtimePingModel)
		err = helpers.DecodeStrictObject(message.Payload, decoded.Ping, nil, "pingId")
		if err == nil && (decoded.Ping.PingID == "" || len(decoded.Ping.PingID) > 128 ||
			strings.TrimSpace(decoded.Ping.PingID) != decoded.Ping.PingID) {
			err = ErrInvalidClientMessage
		}
	case game != nil:
		switch lane := game.(type) {
		case module.TickModule:
			decoded.Intent, err = lane.DecodeIntent(message.Type, message.Payload)
		case module.TurnModule:
			decoded.Move, err = lane.DecodeMove(message.Type, message.Payload)
		default:
			err = module.ErrUnsupportedIntent
		}
		if errors.Is(err, module.ErrUnsupportedIntent) {
			err = ErrUnsupportedRoomCommand
		}
	default:
		err = ErrUnsupportedRoomCommand
	}
	if err != nil {
		return decoded, errors.Join(ErrInvalidClientMessage, err)
	}
	return decoded, nil
}

// tickProtocolError maps runtime rejections to the game's namespaced codes.
func tickProtocolError(gameKey string, err error) ProtocolError {
	code := ""
	switch {
	case errors.Is(err, module.ErrStaleInput), errors.Is(err, module.ErrControlRequired):
		code = "stale_control"
	case errors.Is(err, module.ErrPlayerEliminated):
		code = "player_eliminated"
	case errors.Is(err, module.ErrPlayerNotFound):
		code = "control_unavailable"
	case errors.Is(err, module.ErrFinished), errors.Is(err, ErrRoomRuntimeNotStarted):
		code = "not_running"
	case errors.Is(err, module.ErrInputRate):
		return ProtocolError{Code: messageRateLimitErrorCode}
	case errors.Is(err, module.ErrRuntimeBusy):
		return ProtocolError{Code: roomUnavailableErrorCode, Retryable: true}
	case errors.Is(err, module.ErrInvalidInput), errors.Is(err, module.ErrInputExpired):
		code = "invalid_input"
	}
	if code != "" {
		return ProtocolError{Code: module.ErrorCode(gameKey, code)}
	}
	return protocolErrorFrom(err)
}
