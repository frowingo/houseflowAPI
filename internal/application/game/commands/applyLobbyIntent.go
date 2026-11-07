package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	gameApplication "houseflowApi/internal/application/game"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	housePolicies "houseflowApi/internal/application/house/policies"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

// ApplyLobbyIntentCommand is one player's lobby choice in a game with lobby
// intents (module.LobbyModule). Like a move, it is durable: the new lobby
// state commits with the command's receipt, so a retry applies once.
// IntentType is the client message type the module decodes.
type ApplyLobbyIntentCommand struct {
	cqrs.Request[gameDomain.SessionSnapshot]
	CommandID  string
	SessionID  string
	UserID     string
	IntentType string
	Payload    json.RawMessage
}

type ApplyLobbyIntentHandler struct {
	repository       gameAbstract.GameSessionRepository
	membershipPolicy *housePolicies.MembershipPolicy
	games            *module.Registry
}

func NewApplyLobbyIntentHandler(repository gameAbstract.GameSessionRepository, membershipPolicy *housePolicies.MembershipPolicy, games *module.Registry) *ApplyLobbyIntentHandler {
	return &ApplyLobbyIntentHandler{repository: repository, membershipPolicy: membershipPolicy, games: games}
}

func (handler *ApplyLobbyIntentHandler) Handle(ctx context.Context, command ApplyLobbyIntentCommand) (gameDomain.SessionSnapshot, error) {
	descriptor, err := describeCommand(command.CommandID, command.UserID, command.IntentType, struct {
		SessionID string
		Payload   json.RawMessage
	}{command.SessionID, command.Payload})
	if err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	session, err := handler.repository.FindByID(ctx, command.SessionID)
	if err != nil {
		return gameDomain.SessionSnapshot{}, gameApplication.MapError(err)
	}
	snapshot := session.Snapshot()
	game, exists := handler.games.Lobby(snapshot.GameKey)
	if !exists {
		return gameDomain.SessionSnapshot{}, module.ErrUnsupportedIntent
	}
	intent, err := game.DecodeLobbyIntent(command.IntentType, command.Payload)
	if err != nil {
		return gameDomain.SessionSnapshot{}, mapLobbyError(snapshot.GameKey, err)
	}
	if _, err := handler.membershipPolicy.RequireMember(ctx, snapshot.HouseID, command.UserID); err != nil {
		return gameDomain.SessionSnapshot{}, err
	}
	if snapshot, found, err := processedSnapshot(ctx, handler.repository, descriptor); err != nil || found {
		return snapshot, err
	}
	if !inLobby(snapshot, command.UserID) {
		return gameDomain.SessionSnapshot{}, notInLobby(snapshot.GameKey)
	}
	expectedVersion := session.Version()
	state, err := game.ApplyLobbyIntent(snapshot, command.UserID, command.IntentType, intent)
	if err != nil {
		return gameDomain.SessionSnapshot{}, mapLobbyError(snapshot.GameKey, err)
	}
	// One canonical form, so an unchanged choice adds no version.
	state, err = helpers.CanonicalJSONObject(state)
	if err != nil || state == nil {
		return gameDomain.SessionSnapshot{}, errors.Join(gameDomain.ErrLobbyStateRequired, err)
	}
	if !bytes.Equal(state, snapshot.LobbyState) {
		if err := session.UpdateLobbyState(command.UserID, state, laterOf(time.Now(), snapshot.UpdatedAt)); err != nil {
			return gameDomain.SessionSnapshot{}, mapLobbyError(snapshot.GameKey, err)
		}
	}
	return saveSession(ctx, handler.repository, session, expectedVersion, descriptor)
}

// inLobby reports whether the session still takes lobby choices from the
// player: in the lobby or ready window, and joined.
func inLobby(snapshot gameDomain.SessionSnapshot, playerID string) bool {
	if snapshot.State != gameDomain.SessionLobby && snapshot.State != gameDomain.SessionReadyWindow {
		return false
	}
	for _, player := range snapshot.Players {
		if player.PlayerID == playerID {
			return player.State != gameDomain.PlayerLeft
		}
	}
	return false
}

func notInLobby(gameKey string) error {
	return helpers.NewConflictError(module.ErrorCode(gameKey, "not_in_lobby"))
}

func mapLobbyError(gameKey string, err error) error {
	switch {
	case errors.Is(err, gameDomain.ErrLobbyClosed), errors.Is(err, gameDomain.ErrPlayerNotFound):
		return notInLobby(gameKey)
	case errors.Is(err, module.ErrInvalidInput), errors.Is(err, module.ErrUnsupportedIntent):
		return helpers.NewLocalizedError(module.ErrorCode(gameKey, "invalid_input"))
	default:
		return gameApplication.MapError(err)
	}
}

var _ cqrs.CommandHandler[ApplyLobbyIntentCommand, gameDomain.SessionSnapshot] = (*ApplyLobbyIntentHandler)(nil)
