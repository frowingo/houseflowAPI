package realtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	"houseflowApi/internal/helpers"
)

const (
	ProtocolVersion                 = 1
	CommandAcceptedMessageType      = "gameSession.commandAccepted"
	CommandRejectedMessageType      = "gameSession.commandRejected"
	defaultInvalidCommandErrorCode  = "realtime.error.invalid_command"
	defaultInternalCommandErrorCode = "realtime.error.command_failed"
	unsupportedProtocolErrorCode    = "realtime.error.unsupported_protocol"
	messageRateLimitErrorCode       = "realtime.error.rate_limited"
	roomUnavailableErrorCode        = "realtime.error.unavailable"
	maximumMessageIDLength          = 128
)

var (
	ErrRoomCommandPayloadRequired = errors.New("room command payload is required")
	ErrInvalidClientMessage       = errors.New("invalid realtime client message")
	ErrUnsupportedProtocol        = errors.New("unsupported realtime protocol version")
)

type ClientMessage struct {
	ProtocolVersion int             `json:"protocolVersion"`
	MessageID       string          `json:"messageId"`
	Type            string          `json:"type"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

type ServerMessage struct {
	ProtocolVersion int             `json:"protocolVersion"`
	MessageID       string          `json:"messageId,omitempty"`
	Type            string          `json:"type"`
	Sequence        int64           `json:"sequence,omitempty"`
	SentAt          time.Time       `json:"sentAt"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	Error           *ProtocolError  `json:"error,omitempty"`
}

type ProtocolError struct {
	Code      string   `json:"code"`
	Args      []string `json:"args,omitempty"`
	Retryable bool     `json:"retryable"`
}

func decodeClientMessage(payload []byte) (ClientMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var message ClientMessage
	if err := decoder.Decode(&message); err != nil {
		return ClientMessage{}, errors.Join(ErrInvalidClientMessage, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return ClientMessage{}, errors.Join(ErrInvalidClientMessage, err)
	}
	message.MessageID = strings.TrimSpace(message.MessageID)
	message.Type = strings.TrimSpace(message.Type)
	if message.ProtocolVersion != ProtocolVersion {
		return message, ErrUnsupportedProtocol
	}
	if message.MessageID == "" ||
		len(message.MessageID) > maximumMessageIDLength ||
		!isSupportedClientCommand(message.Type) {
		return message, ErrInvalidClientMessage
	}
	return message, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values are not allowed")
	}
	return err
}

func isSupportedClientCommand(commandType string) bool {
	switch commandType {
	case gameCommands.JoinGameSessionCommandType,
		gameCommands.SetPlayerReadyCommandType,
		gameCommands.LeaveGameSessionCommandType,
		gameCommands.CancelGameSessionCommandType:
		return true
	default:
		return false
	}
}

func protocolErrorFrom(err error) ProtocolError {
	var applicationError *helpers.ApplicationError
	if errors.As(err, &applicationError) {
		return ProtocolError{
			Code:      applicationError.Key,
			Args:      append([]string(nil), applicationError.Args...),
			Retryable: applicationError.Kind == helpers.ErrorKindUnavailable,
		}
	}
	if errors.Is(err, ErrUnsupportedRoomCommand) ||
		errors.Is(err, ErrRoomCommandPayloadRequired) ||
		errors.Is(err, ErrInvalidClientMessage) {
		return ProtocolError{Code: defaultInvalidCommandErrorCode}
	}
	if errors.Is(err, ErrUnsupportedProtocol) {
		return ProtocolError{Code: unsupportedProtocolErrorCode}
	}
	return ProtocolError{Code: defaultInternalCommandErrorCode, Retryable: true}
}

func newServerMessage(messageType string, messageID string) ServerMessage {
	return ServerMessage{
		ProtocolVersion: ProtocolVersion,
		MessageID:       messageID,
		Type:            messageType,
		SentAt:          time.Now().UTC(),
	}
}
