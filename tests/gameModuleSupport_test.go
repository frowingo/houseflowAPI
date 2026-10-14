package tests

import (
	"context"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/application/game/module"
	gameQueries "houseflowApi/internal/application/game/queries"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/realtime"
)

// rocketsGames is the registry of a process with House Rockets enabled.
func rocketsGames() *module.Registry {
	registry, err := games.NewRegistry(games.Options{EnableHouseRockets: true})
	if err != nil {
		panic(err)
	}
	return registry
}

// builtinCatalog is the catalog a process advertises for the given options.
func builtinCatalog(options games.Options) (*gameApplication.Catalog, error) {
	registry, err := games.NewRegistry(options)
	if err != nil {
		return nil, err
	}
	return gameApplication.NewCatalog(registry.Definitions()...)
}

// registerRocketsResults adds the shared completion and result handlers.
func registerRocketsResults(mediator *cqrs.Mediator, sessions gameAbstract.GameSessionRepository, matches gameAbstract.GameMatchRepository, membership *housePolicies.MembershipPolicy) {
	cqrs.MustRegister[gameDomain.MatchResult, gameCommands.CompleteMatchCommand](mediator, gameCommands.NewCompleteMatchHandler(sessions, matches, rocketsGames()))
	if membership != nil {
		cqrs.MustRegister[any, gameQueries.GetMatchResultQuery](mediator, gameQueries.NewGetMatchResultHandler(sessions, matches, rocketsGames(), membership))
	}
}

// rocketsCompletion is a CompleteMatchCommand expressed in House Rockets'
// result model, so completion tests can assert on game fields.
type rocketsCompletion struct {
	Result          houseRockets.HouseRocketsResultModel
	Owner           gameAbstract.RuntimeOwner
	CancelCommandID string
	CancelUserID    string
}

type rocketsCompletionHandler struct {
	handler *gameCommands.CompleteMatchHandler
}

func newRocketsCompletionHandler(sessions gameAbstract.GameSessionRepository, matches gameAbstract.GameMatchRepository) rocketsCompletionHandler {
	return rocketsCompletionHandler{gameCommands.NewCompleteMatchHandler(sessions, matches, rocketsGames())}
}

func (completion rocketsCompletionHandler) Handle(ctx context.Context, command rocketsCompletion) (houseRockets.HouseRocketsResultModel, error) {
	proposed, err := houseRockets.EncodeMatchResult(command.Result)
	if err != nil {
		return houseRockets.HouseRocketsResultModel{}, err
	}
	stored, err := completion.handler.Handle(ctx, gameCommands.CompleteMatchCommand{Result: proposed, Owner: command.Owner, CancelCommandID: command.CancelCommandID, CancelUserID: command.CancelUserID})
	if err != nil {
		return houseRockets.HouseRocketsResultModel{}, err
	}
	return houseRockets.DecodeMatchResult(stored)
}

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
