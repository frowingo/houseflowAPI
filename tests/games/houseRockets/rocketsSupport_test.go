package houseRockets

import (
	"context"

	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/infrastructure/realtime"
)

// rocketsInput carries a complete House Rockets runtime input; the dispatching
// manager supplies the authenticated identity.
type rocketsInput struct{ input houseRockets.RuntimeInput }

func (intent rocketsInput) RuntimeInput(control module.ControlInput) any {
	input := intent.input
	input.MessageID, input.PlayerID, input.ConnectionID, input.CreatedAt = control.MessageID, control.PlayerID, control.ConnectionID, control.CreatedAt
	return input
}

func dispatchRockets(manager *realtime.RoomManager, ctx context.Context, roomID, actorID, connectionID string, input houseRockets.RuntimeInput) error {
	control := module.ControlInput{MessageID: input.MessageID, Kind: input.Kind, PlayerID: actorID, ConnectionID: connectionID, RuntimeEpoch: input.RuntimeEpoch, ControlGeneration: input.ControlGeneration, InputSequence: input.InputSequence}
	return manager.DispatchGameplay(ctx, roomID, control, rocketsInput{input})
}

// decodedSteer reads a decoded steer intent back as the client's wire model.
func decodedSteer(decoded realtime.DecodedV2ClientMessage) *houseRockets.HouseRocketsSteerModel {
	if decoded.Intent == nil {
		return nil
	}
	input := decoded.Intent.RuntimeInput(module.ControlInput{}).(houseRockets.RuntimeInput)
	return &houseRockets.HouseRocketsSteerModel{ControlGeneration: input.ControlGeneration, InputSequence: input.InputSequence, Heading: input.Heading}
}
