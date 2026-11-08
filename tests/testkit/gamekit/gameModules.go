package gamekit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/games"
	fausSwitch "houseflowApi/internal/application/game/games/fausSwitch"
	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/application/game/module"
	gameQueries "houseflowApi/internal/application/game/queries"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/infrastructure/cqrs"
)

func RunningContractSession(t *testing.T, definition gameDomain.GameDefinition) gameDomain.SessionSnapshot {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	rules := definition.Rules
	rules.ReadyWindowDuration, rules.CountdownDuration = 0, 0
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "contract-session", HouseID: "contract-house", GameKey: definition.GameKey, ProtocolVersion: definition.ProtocolVersion, Mode: definition.Mode, Rules: rules, CreatedBy: "contract-a", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"contract-a", "contract-b"} {
		if err := session.JoinPlayer(id, now); err != nil {
			t.Fatal(err)
		}
		if err := session.SetReady(id, true, now); err != nil {
			t.Fatal(err)
		}
	}
	if session.State() != gameDomain.SessionRunning {
		t.Fatalf("contract session state = %s", session.State())
	}
	return session.Snapshot()
}

func DecodeFrameHeader(t *testing.T, payload json.RawMessage) module.FrameHeader {
	t.Helper()
	var header module.FrameHeader
	if err := json.Unmarshal(payload, &header); err != nil {
		t.Fatal(err)
	}
	return header
}

// rocketsGames is the registry of a process with House Rockets enabled.
func RocketsGames() *module.Registry {
	registry, err := games.NewRegistry(games.Options{Realtime: true})
	if err != nil {
		panic(err)
	}
	return registry
}

// builtinCatalog is the catalog a process advertises for the given options.
func BuiltinCatalog(options games.Options) (*gameApplication.Catalog, error) {
	registry, err := games.NewRegistry(options)
	if err != nil {
		return nil, err
	}
	return gameApplication.NewCatalog(registry, nil, nil), nil
}

// definitionCatalog serves fixed definitions, enabled with their own rules and
// default settings, for tests of the game-agnostic session layer.
type DefinitionCatalog map[string]gameDomain.GameDefinition

func NewDefinitionCatalog(definitions ...gameDomain.GameDefinition) (DefinitionCatalog, error) {
	catalog := make(DefinitionCatalog, len(definitions))
	for _, definition := range definitions {
		if err := definition.Validate(); err != nil {
			return nil, err
		}
		catalog[definition.GameKey] = definition
	}
	return catalog, nil
}

func (catalog DefinitionCatalog) Find(gameKey string) (gameDomain.GameDefinition, error) {
	definition, exists := catalog[gameKey]
	if !exists {
		return gameDomain.GameDefinition{}, gameAbstract.ErrGameDefinitionNotFound
	}
	return definition, nil
}

func (catalog DefinitionCatalog) Config(gameKey string) (gameDomain.GameConfig, error) {
	definition, err := catalog.Find(gameKey)
	return gameDomain.GameConfig{Enabled: true, Rules: definition.Rules}, err
}

// registerRocketsResults adds the shared completion and result handlers.
func RegisterRocketsResults(mediator *cqrs.Mediator, sessions gameAbstract.GameSessionRepository, matches gameAbstract.GameMatchRepository, membership *housePolicies.MembershipPolicy) {
	cqrs.MustRegister[gameDomain.MatchResult, gameCommands.CompleteMatchCommand](mediator, gameCommands.NewCompleteMatchHandler(sessions, matches, RocketsGames()))
	if membership != nil {
		cqrs.MustRegister[any, gameQueries.GetMatchResultQuery](mediator, gameQueries.NewGetMatchResultHandler(sessions, matches, RocketsGames(), membership))
	}
}

// rocketsCompletion is a CompleteMatchCommand expressed in House Rockets'
// result model, so completion tests can assert on game fields.
type RocketsCompletion struct {
	Result          houseRockets.HouseRocketsResultModel
	Owner           gameAbstract.RuntimeOwner
	CancelCommandID string
	CancelUserID    string
}

type RocketsCompletionHandler struct {
	handler *gameCommands.CompleteMatchHandler
}

func NewRocketsCompletionHandler(sessions gameAbstract.GameSessionRepository, matches gameAbstract.GameMatchRepository) RocketsCompletionHandler {
	return RocketsCompletionHandler{gameCommands.NewCompleteMatchHandler(sessions, matches, RocketsGames())}
}

func (completion RocketsCompletionHandler) Handle(ctx context.Context, command RocketsCompletion) (houseRockets.HouseRocketsResultModel, error) {
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

func ColorsOf(t *testing.T, state json.RawMessage) map[string]fausSwitch.Color {
	t.Helper()
	var lobby struct {
		Colors map[string]fausSwitch.Color `json:"colors"`
	}
	if len(state) > 0 {
		if err := json.Unmarshal(state, &lobby); err != nil {
			t.Fatal(err)
		}
	}
	return lobby.Colors
}
