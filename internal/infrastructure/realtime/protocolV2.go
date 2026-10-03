package realtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	gameCommands "houseflowApi/internal/application/game/commands"
	"houseflowApi/internal/application/game/gameSpesific/houseRockets"
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
	Steer    *houseRockets.HouseRocketsSteerModel
	Ping     *dtos.RealtimePingModel
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

func V2ProtocolErrorFrom(err error) V2ProtocolError {
	if errors.Is(err, houseRockets.ErrInvalidInput) {
		return V2ProtocolError{Code: houseRockets.InvalidInputErrorCode, Args: []string{}}
	}
	result := protocolErrorFrom(err)
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

func DecodeV2ClientMessage(data []byte) (DecodedV2ClientMessage, error) {
	var decoded DecodedV2ClientMessage
	if len(data) > defaultMaximumMessageBytes {
		return decoded, ErrInvalidClientMessage
	}
	if err := decodeV2Object(data, &decoded.Envelope, []string{"payload"}, "protocolVersion", "messageId", "type"); err != nil {
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
	switch message.Type {
	case gameCommands.JoinGameSessionCommandType, gameCommands.LeaveGameSessionCommandType,
		gameCommands.CancelGameSessionCommandType, houseRockets.ResyncMessageType:
		if len(message.Payload) > 0 {
			var empty struct{}
			err = decodeV2Object(message.Payload, &empty, nil)
		}
	case gameCommands.SetPlayerReadyCommandType:
		decoded.Ready = new(dtos.RealtimeReadyModel)
		err = decodeV2Object(message.Payload, decoded.Ready, nil, "ready")
	case houseRockets.SteerMessageType:
		decoded.Steer = new(houseRockets.HouseRocketsSteerModel)
		err = decodeV2Object(message.Payload, decoded.Steer, nil, "controlGeneration", "inputSequence", "heading")
		if err == nil && !decoded.Steer.Valid() {
			err = houseRockets.ErrInvalidInput
		}
		if err == nil {
			decoded.Steer.Heading = math.Atan2(math.Sin(decoded.Steer.Heading), math.Cos(decoded.Steer.Heading))
		} else {
			err = errors.Join(houseRockets.ErrInvalidInput, err)
		}
	case PingMessageType:
		decoded.Ping = new(dtos.RealtimePingModel)
		err = decodeV2Object(message.Payload, decoded.Ping, nil, "pingId")
		if err == nil && (decoded.Ping.PingID == "" || len(decoded.Ping.PingID) > 128 ||
			strings.TrimSpace(decoded.Ping.PingID) != decoded.Ping.PingID) {
			err = ErrInvalidClientMessage
		}
	default:
		err = ErrUnsupportedRoomCommand
	}
	if err != nil {
		return decoded, errors.Join(ErrInvalidClientMessage, err)
	}
	return decoded, nil
}

func decodeV2Object(data []byte, target any, optionalFields []string, requiredFields ...string) error {
	if err := rejectDuplicateV2Keys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("JSON object is required")
	}
	for _, field := range requiredFields {
		value, exists := fields[field]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s is required", field)
		}
	}
	allowedFields := make(map[string]bool, len(requiredFields)+len(optionalFields))
	for _, field := range requiredFields {
		allowedFields[field] = true
	}
	for _, field := range optionalFields {
		allowedFields[field] = true
	}
	for field := range fields {
		if !allowedFields[field] {
			return fmt.Errorf("unknown field %s", field)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEnd(decoder)
}

func rejectDuplicateV2Keys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var readValue func() error
	readValue = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, composite := token.(json.Delim)
		if !composite {
			return nil
		}
		seen := make(map[string]bool)
		for decoder.More() {
			if delimiter == '{' {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errors.New("duplicate or invalid JSON field")
				}
				seen[key] = true
			}
			if err := readValue(); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	if err := readValue(); err != nil {
		return err
	}
	return ensureJSONEnd(decoder)
}
