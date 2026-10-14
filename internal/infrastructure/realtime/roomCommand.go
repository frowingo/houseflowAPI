package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
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
		// Any other type can only be a turn game's move; the module decides.
		snapshot, err := cqrs.Send[gameDomain.SessionSnapshot](ctx, processor.sender, gameCommands.ApplyTurnMoveCommand{
			CommandID: envelope.MessageID,
			SessionID: envelope.RoomID,
			UserID:    envelope.ActorID,
			MoveType:  envelope.Type,
			Payload:   envelope.Payload,
		})
		var missing *cqrs.HandlerNotFoundError
		if errors.Is(err, module.ErrUnsupportedIntent) || errors.As(err, &missing) {
			return gameDomain.SessionSnapshot{}, ErrUnsupportedRoomCommand
		}
		return snapshot, err
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
	if err := helpers.EnsureJSONEnd(decoder); err != nil {
		return value, err
	}
	return value, nil
}
