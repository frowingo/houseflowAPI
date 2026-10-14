package realtime

import (
	"encoding/json"
	"errors"
	"io"
	"time"

	"houseflowApi/internal/helpers"
)

const (
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

// ServerMessage is the decoded shape shared by every server envelope,
// including V2ServerMessage and V2ErrorMessage.
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
