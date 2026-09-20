package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/infrastructure/cqrs"
)

var ErrUnsupportedRoomCommand = errors.New("unsupported room command")

type ReadyCommandPayload struct {
	Ready bool `json:"ready"`
}

type commandProcessor struct {
	sender cqrs.Sender
}

func newCommandProcessor(sender cqrs.Sender) commandProcessor {
	return commandProcessor{sender: sender}
}

func (processor commandProcessor) Process(
	ctx context.Context,
	envelope coordinationAbstract.MessageEnvelope,
) (gameDomain.SessionSnapshot, error) {
	switch envelope.Type {
	case gameCommands.JoinGameSessionCommandType:
		return cqrs.Send[gameDomain.SessionSnapshot](ctx, processor.sender, gameCommands.JoinGameSessionCommand{
			CommandID: envelope.MessageID,
			SessionID: envelope.RoomID,
			UserID:    envelope.ActorID,
		})
	case gameCommands.SetPlayerReadyCommandType:
		payload, err := decodePayload[ReadyCommandPayload](envelope.Payload)
		if err != nil {
			return gameDomain.SessionSnapshot{}, err
		}
		return cqrs.Send[gameDomain.SessionSnapshot](ctx, processor.sender, gameCommands.SetPlayerReadyCommand{
			CommandID: envelope.MessageID,
			SessionID: envelope.RoomID,
			UserID:    envelope.ActorID,
			Ready:     payload.Ready,
		})
	case gameCommands.LeaveGameSessionCommandType:
		return cqrs.Send[gameDomain.SessionSnapshot](ctx, processor.sender, gameCommands.LeaveGameSessionCommand{
			CommandID: envelope.MessageID,
			SessionID: envelope.RoomID,
			UserID:    envelope.ActorID,
		})
	case gameCommands.CancelGameSessionCommandType:
		return cqrs.Send[gameDomain.SessionSnapshot](ctx, processor.sender, gameCommands.CancelGameSessionCommand{
			CommandID: envelope.MessageID,
			SessionID: envelope.RoomID,
			UserID:    envelope.ActorID,
		})
	default:
		return gameDomain.SessionSnapshot{}, ErrUnsupportedRoomCommand
	}
}

func decodePayload[T any](payload json.RawMessage) (T, error) {
	var value T
	if len(payload) == 0 {
		return value, ErrRoomCommandPayloadRequired
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return value, err
	}
	return value, nil
}
